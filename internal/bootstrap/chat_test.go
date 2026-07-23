package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
)

func TestChatStreamDeliversABroadcastEventAsSSE(t *testing.T) {
	hub := webchat.NewHub()
	handler := newChatStreamHandler(hub)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler(response, request)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond) // let the handler register with the hub
	hub.Broadcast(webchat.Event{Kind: webchat.EventMessage, ID: "1", Text: "hello"})

	<-done
	body := response.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"text":"hello"`) {
		t.Fatalf("body=%q", body)
	}
}

func TestChatStreamSetsSSEHeaders(t *testing.T) {
	hub := webchat.NewHub()
	handler := newChatStreamHandler(hub)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type=%q", response.Header().Get("Content-Type"))
	}
}
