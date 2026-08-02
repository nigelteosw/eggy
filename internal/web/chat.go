package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/webchat"
)

// ThreadDirectory and HistoryReader are what the chat routes need from the
// thread and memory stores, declared here rather than taken as the full
// ports.ThreadStore and ports.MemoryStore. The narrowing is not cosmetic:
// ports.ThreadStore also carries workspace lifecycle methods, and
// ports.MemoryStore also carries writing and search methods. Workspace
// lifecycle belongs to the turn path; an HTTP handler holding either whole
// interface could reach unrelated operations by accident. The concrete
// stores satisfy these narrower interfaces structurally.
type ThreadDirectory interface {
	CreateThread(ctx context.Context, id, channel string, at time.Time) (ports.Thread, error)
	ListThreads(ctx context.Context, channel string) ([]ports.Thread, error)
	GetThread(ctx context.Context, id string) (thread ports.Thread, found bool, err error)
	RenameThread(ctx context.Context, id, title string) error
	DeleteThread(ctx context.Context, id string) error
}

// HistoryReader reads a thread's messages back for display. Writing is the
// turn path's job, so this deliberately cannot.
type HistoryReader interface {
	RecentMessages(ctx context.Context, conversationID string, limit int) ([]ports.StoredMessage, error)
}

// ChatStream is the live event fan-out a streaming connection subscribes to.
// The web surface only ever subscribes; publishing belongs to the channel
// adapter that owns the hub.
type ChatStream interface {
	Register(threadID string) (connID string, events <-chan webchat.Event, unregister func())
}

const chatKeepaliveInterval = 15 * time.Second

// chatHistoryDisplayLimit bounds how many of a thread's most recent
// messages the history route returns for display. It is independent of
// (and larger than) ConversationService's recentLimit, which bounds only
// the live agent turn-context window.
const chatHistoryDisplayLimit = 200

// buildWebEvent stamps a new events.Event with the same ID/Source/Timestamp/
// CorrelationID shape Telegram's webhook handler already uses (see
// plugins/channels/telegram/handler.go's normalize), and the
// Owner every event must carry for Dispatcher.Handle to accept it. This is
// shared by newThreadSendHandler and newChatApproveHandler.
func buildWebEvent(owner string, eventType events.Type, dest destination.Destination, payload json.RawMessage) events.Event {
	id := "web:" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	return events.Event{
		ID: id, Type: eventType, Source: "web", Owner: owner,
		Timestamp: time.Now().UTC(), CorrelationID: id, Destination: dest, Payload: payload,
	}
}

func newThreadID() string {
	data := make([]byte, 8)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

// requireExistingThread looks up id (from the URL) and writes a 404 if it
// doesn't exist, so a deleted-out-from-under-an-open-tab or malformed
// thread ID never reaches the handler's real work. Returns ok=false when
// the response has already been written.
func requireExistingThread(w http.ResponseWriter, r *http.Request, threads ThreadDirectory) (id string, ok bool) {
	id = r.PathValue("id")
	if _, found, err := threads.GetThread(r.Context(), id); err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return "", false
	} else if !found {
		writeWebError(w, http.StatusNotFound, "thread not found")
		return "", false
	}
	return id, true
}

func newThreadListHandler(threads ThreadDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		threads, err := threads.ListThreads(r.Context(), "web")
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([][]string, 0, len(threads))
		for _, thread := range threads {
			rows = append(rows, []string{thread.ID, thread.Title, thread.UpdatedAt.Format(time.RFC3339)})
		}
		writeWebResult(w, webResult{
			State:        webSuccess,
			TableHeaders: []string{"id", "title", "updated_at"},
			TableRows:    rows,
		})
	}
}

func newThreadCreateHandler(threads ThreadDirectory, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		thread, err := threads.CreateThread(r.Context(), newThreadID(), "web", now())
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		body, _ := json.Marshal(struct {
			ID string `json:"id"`
		}{ID: thread.ID})
		_, _ = w.Write(body)
	}
}

// threadTitleMaxLength bounds an owner-supplied title. The sidebar truncates
// long titles anyway, so the limit exists to keep the stored row sane rather
// than to make anything fit.
const threadTitleMaxLength = 200

func newThreadRenameHandler(threads ThreadDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, threads)
		if !ok {
			return
		}
		var input struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		title := strings.TrimSpace(input.Title)
		if title == "" {
			writeWebError(w, http.StatusBadRequest, "title is required")
			return
		}
		if len(title) > threadTitleMaxLength {
			writeWebError(w, http.StatusBadRequest, "title is too long")
			return
		}
		if err := threads.RenameThread(r.Context(), id, title); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Chat renamed."})
	}
}

// newThreadDeleteHandler deletes a thread and its messages. A thread with a
// workspace attached is refused rather than silently orphaning the checkout
// on disk: the reaper only ever sees workspaces through thread rows, so a
// deleted row is a checkout nothing will ever clean up.
func newThreadDeleteHandler(threads ThreadDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		thread, found, err := threads.GetThread(r.Context(), id)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeWebError(w, http.StatusNotFound, "thread not found")
			return
		}
		if thread.Workspace != "" {
			writeWebError(w, http.StatusConflict, "this chat has a workspace attached; close it before deleting")
			return
		}
		if err := threads.DeleteThread(r.Context(), id); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Chat deleted."})
	}
}

func newThreadHistoryHandler(threads ThreadDirectory, memory HistoryReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, threads)
		if !ok {
			return
		}
		messages, err := memory.RecentMessages(r.Context(), id, chatHistoryDisplayLimit)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([][]string, 0, len(messages))
		for _, message := range messages {
			rows = append(rows, []string{string(message.Role), message.Content})
		}
		writeWebResult(w, webResult{
			State:        webSuccess,
			TableHeaders: []string{"role", "content"},
			TableRows:    rows,
		})
	}
}

func newThreadSendHandler(enqueue func(context.Context, events.Event) error, owner string, threads ThreadDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, threads)
		if !ok {
			return
		}
		var input struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(input.Text) == "" {
			writeWebError(w, http.StatusBadRequest, "text is required")
			return
		}
		payload, err := json.Marshal(events.Message{Text: input.Text})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "failed to encode message")
			return
		}
		event := buildWebEvent(owner, events.TypeMessage, destination.Destination{Kind: destination.Web, ThreadID: id}, payload)
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := json.Marshal(webResult{State: webSuccess, Title: "Message received."})
		_, _ = w.Write(body)
	}
}

func newChatApproveHandler(enqueue func(context.Context, events.Event) error, owner string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ApprovalID string `json:"approval_id"`
			Approved   bool   `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(input.ApprovalID) == "" {
			writeWebError(w, http.StatusBadRequest, "approval_id is required")
			return
		}
		payload, err := json.Marshal(events.ApprovalDecision{ApprovalID: input.ApprovalID, Approved: input.Approved})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "failed to encode decision")
			return
		}
		event := buildWebEvent(owner, events.TypeApproval, destination.Destination{}, payload)
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := json.Marshal(webResult{State: webSuccess, Title: "Decision received."})
		_, _ = w.Write(body)
	}
}

func newThreadStreamHandler(hub ChatStream, threads ThreadDirectory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, threads)
		if !ok {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		_, events, unregister := hub.Register(id)
		defer unregister()

		keepalive := time.NewTicker(chatKeepaliveInterval)
		defer keepalive.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case event, ok := <-events:
				if !ok {
					return
				}
				body, err := json.Marshal(event)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Kind, body); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
