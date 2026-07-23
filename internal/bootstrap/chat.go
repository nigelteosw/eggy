package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

const chatKeepaliveInterval = 15 * time.Second

// buildWebEvent stamps a new events.Event with the same ID/Source/Timestamp/
// CorrelationID shape Telegram's webhook handler already uses (see
// internal/adapters/channels/telegram/handler.go's normalize), and the
// Owner every event must carry for Dispatcher.Handle to accept it. This is
// shared by newChatSendHandler and newChatApproveHandler.
func buildWebEvent(owner string, eventType events.Type, payload json.RawMessage) events.Event {
	id := "web:" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	return events.Event{
		ID: id, Type: eventType, Source: "web", Owner: owner,
		Timestamp: time.Now().UTC(), CorrelationID: id, Payload: payload,
	}
}

func newChatSendHandler(enqueue func(context.Context, events.Event) error, owner string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

func newChatStreamHandler(hub *webchat.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		_, events, unregister := hub.Register()
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
