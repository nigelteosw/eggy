package bootstrap

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

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	memorysqlite "github.com/nigelteosw/eggy/internal/adapters/memory/sqlite"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

const chatKeepaliveInterval = 15 * time.Second

// chatHistoryDisplayLimit bounds how many of a thread's most recent
// messages the history route returns for display. It is independent of
// (and larger than) ConversationService's recentLimit, which bounds only
// the live agent turn-context window.
const chatHistoryDisplayLimit = 200

// buildWebEvent stamps a new events.Event with the same ID/Source/Timestamp/
// CorrelationID shape Telegram's webhook handler already uses (see
// internal/adapters/channels/telegram/handler.go's normalize), and the
// Owner every event must carry for Dispatcher.Handle to accept it. This is
// shared by newThreadSendHandler and newChatApproveHandler.
func buildWebEvent(owner string, eventType events.Type, payload json.RawMessage) events.Event {
	id := "web:" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	return events.Event{
		ID: id, Type: eventType, Source: "web", Owner: owner,
		Timestamp: time.Now().UTC(), CorrelationID: id, Payload: payload,
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
func requireExistingThread(w http.ResponseWriter, r *http.Request, memory *memorysqlite.Store) (id string, ok bool) {
	id = r.PathValue("id")
	if _, found, err := memory.GetThread(r.Context(), id); err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return "", false
	} else if !found {
		writeWebError(w, http.StatusNotFound, "thread not found")
		return "", false
	}
	return id, true
}

func newThreadListHandler(memory *memorysqlite.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		threads, err := memory.ListThreads(r.Context(), "web")
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows := make([][]string, 0, len(threads))
		for _, thread := range threads {
			rows = append(rows, []string{thread.ID, thread.Title, thread.UpdatedAt.Format(time.RFC3339)})
		}
		writeWebResult(w, CommandResult{
			State:        ResultSuccess,
			TableHeaders: []string{"id", "title", "updated_at"},
			TableRows:    rows,
		})
	}
}

func newThreadCreateHandler(memory *memorysqlite.Store, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		thread, err := memory.CreateThread(r.Context(), newThreadID(), "web", now())
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

func newThreadHistoryHandler(memory *memorysqlite.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, memory)
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
		writeWebResult(w, CommandResult{
			State:        ResultSuccess,
			TableHeaders: []string{"role", "content"},
			TableRows:    rows,
		})
	}
}

func newThreadSendHandler(enqueue func(context.Context, events.Event) error, owner string, memory *memorysqlite.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, memory)
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
		payload, err := json.Marshal(events.Message{ChatID: id, Text: input.Text})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "failed to encode message")
			return
		}
		event := buildWebEvent(owner, events.TypeMessage, payload)
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := (CommandResult{State: ResultSuccess, Title: "Message received."}).RenderJSON()
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
		event := buildWebEvent(owner, events.TypeApproval, payload)
		if err := enqueue(r.Context(), event); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		body, _ := (CommandResult{State: ResultSuccess, Title: "Decision received."}).RenderJSON()
		_, _ = w.Write(body)
	}
}

func newThreadStreamHandler(hub *webchat.Hub, memory *memorysqlite.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireExistingThread(w, r, memory)
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
