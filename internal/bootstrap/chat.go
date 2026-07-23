package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
)

const chatKeepaliveInterval = 15 * time.Second

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
