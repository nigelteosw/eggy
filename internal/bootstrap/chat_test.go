package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	"github.com/nigelteosw/eggy/internal/kernel/events"
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

func TestChatSendEnqueuesAMessageEventWithTheOwnerSet(t *testing.T) {
	var got events.Event
	enqueue := func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}
	handler := newChatSendHandler(enqueue, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hello Eggy"}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.Type != events.TypeMessage || got.Source != "web" {
		t.Fatalf("event=%#v", got)
	}
	if got.Owner != "owner-42" {
		t.Fatalf("Owner=%q, want the dispatcher's configured owner (Dispatcher.Handle rejects anything else)", got.Owner)
	}
	if got.ID == "" || got.CorrelationID != got.ID {
		t.Fatalf("ID=%q CorrelationID=%q, want both set and equal, matching Telegram's event shape", got.ID, got.CorrelationID)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Text != "hello Eggy" {
		t.Fatalf("message=%#v", message)
	}
}

func TestChatSendRejectsEmptyText(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return nil }, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":""}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestChatSendRejectsInvalidBody(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return nil }, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`not json`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestChatSendReturns500WhenEnqueueFails(t *testing.T) {
	handler := newChatSendHandler(func(context.Context, events.Event) error { return errors.New("queue full") }, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/send", strings.NewReader(`{"text":"hi"}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
}
