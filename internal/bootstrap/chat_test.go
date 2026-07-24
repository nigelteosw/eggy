package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	memorysqlite "github.com/nigelteosw/eggy/internal/adapters/memory/sqlite"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

func newTestMemoryStore(t *testing.T) *memorysqlite.Store {
	t.Helper()
	store, err := memorysqlite.Open(filepath.Join(t.TempDir(), "eggy.db"), 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func withPathValue(request *http.Request, key, value string) *http.Request {
	request.SetPathValue(key, value)
	return request
}

func TestThreadListReturnsOnlyWebThreadsMostRecentlyActiveFirst(t *testing.T) {
	memory := newTestMemoryStore(t)
	base := time.Now()
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", base); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.CreateThread(context.Background(), "thread-2", "web", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.CreateThread(context.Background(), "telegram", "telegram", base); err != nil {
		t.Fatal(err)
	}
	handler := newThreadListHandler(memory)

	request := httptest.NewRequest(http.MethodGet, "/api/chat/threads", nil)
	response := httptest.NewRecorder()
	handler(response, request)

	var decoded CommandResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 2 || decoded.TableRows[0][0] != "thread-2" || decoded.TableRows[1][0] != "thread-1" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}

func TestThreadCreateReturnsANewUntitledThreadID(t *testing.T) {
	memory := newTestMemoryStore(t)
	handler := newThreadCreateHandler(memory, time.Now)

	request := httptest.NewRequest(http.MethodPost, "/api/chat/threads", nil)
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID == "" {
		t.Fatal("expected a non-empty thread ID")
	}
	if _, found, err := memory.GetThread(context.Background(), decoded.ID); err != nil || !found {
		t.Fatalf("thread not persisted: found=%v err=%v", found, err)
	}
}

func TestThreadHistoryReturnsThatThreadsRecentMessagesAsTableRows(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-1", Role: ports.RoleUser, Content: "hi", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "thread-1", Role: ports.RoleAssistant, Content: "hello!", Source: "web", CreatedAt: time.Now().Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteMessage(context.Background(), ports.StoredMessage{
		ConversationID: "other-thread", Role: ports.RoleUser, Content: "not this one", Source: "web", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	handler := newThreadHistoryHandler(memory)

	request := withPathValue(httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-1/history", nil), "id", "thread-1")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded CommandResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 2 || decoded.TableRows[0][0] != "user" || decoded.TableRows[0][1] != "hi" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
	if decoded.TableRows[1][0] != "assistant" || decoded.TableRows[1][1] != "hello!" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}

func TestThreadHistoryReturns404ForAnUnknownThread(t *testing.T) {
	memory := newTestMemoryStore(t)
	handler := newThreadHistoryHandler(memory)

	request := withPathValue(httptest.NewRequest(http.MethodGet, "/api/chat/threads/missing/history", nil), "id", "missing")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestThreadSendEnqueuesAMessageEventScopedToTheThread(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	var got events.Event
	enqueue := func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}
	handler := newThreadSendHandler(enqueue, "owner-42", memory)

	request := withPathValue(httptest.NewRequest(http.MethodPost, "/api/chat/threads/thread-1/send", strings.NewReader(`{"text":"hello Eggy"}`)), "id", "thread-1")
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
	if message.Text != "hello Eggy" || message.ChatID != "thread-1" {
		t.Fatalf("message=%#v, want ChatID set to the thread ID", message)
	}
}

func TestThreadSendReturns404ForAnUnknownThread(t *testing.T) {
	memory := newTestMemoryStore(t)
	handler := newThreadSendHandler(func(context.Context, events.Event) error { return nil }, "owner-42", memory)

	request := withPathValue(httptest.NewRequest(http.MethodPost, "/api/chat/threads/missing/send", strings.NewReader(`{"text":"hi"}`)), "id", "missing")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestThreadSendRejectsEmptyText(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := newThreadSendHandler(func(context.Context, events.Event) error { return nil }, "owner-42", memory)

	request := withPathValue(httptest.NewRequest(http.MethodPost, "/api/chat/threads/thread-1/send", strings.NewReader(`{"text":""}`)), "id", "thread-1")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestThreadSendRejectsInvalidBody(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := newThreadSendHandler(func(context.Context, events.Event) error { return nil }, "owner-42", memory)

	request := withPathValue(httptest.NewRequest(http.MethodPost, "/api/chat/threads/thread-1/send", strings.NewReader(`not json`)), "id", "thread-1")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestThreadSendReturns500WhenEnqueueFails(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := newThreadSendHandler(func(context.Context, events.Event) error { return errors.New("queue full") }, "owner-42", memory)

	request := withPathValue(httptest.NewRequest(http.MethodPost, "/api/chat/threads/thread-1/send", strings.NewReader(`{"text":"hi"}`)), "id", "thread-1")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestThreadStreamDeliversABroadcastEventScopedToThatThreadAsSSE(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	hub := webchat.NewHub()
	handler := newThreadStreamHandler(hub, memory)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	request := withPathValue(httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-1/stream", nil).WithContext(ctx), "id", "thread-1")
	response := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler(response, request)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond) // let the handler register with the hub
	hub.Broadcast("thread-1", webchat.Event{Kind: webchat.EventMessage, ID: "1", Text: "hello"})
	hub.Broadcast("other-thread", webchat.Event{Kind: webchat.EventMessage, ID: "2", Text: "not this thread"})

	<-done
	body := response.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"text":"hello"`) {
		t.Fatalf("body=%q", body)
	}
	if strings.Contains(body, "not this thread") {
		t.Fatalf("body=%q, want no events from another thread", body)
	}
}

func TestThreadStreamSetsSSEHeaders(t *testing.T) {
	memory := newTestMemoryStore(t)
	if _, err := memory.CreateThread(context.Background(), "thread-1", "web", time.Now()); err != nil {
		t.Fatal(err)
	}
	hub := webchat.NewHub()
	handler := newThreadStreamHandler(hub, memory)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := withPathValue(httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-1/stream", nil).WithContext(ctx), "id", "thread-1")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type=%q", response.Header().Get("Content-Type"))
	}
}

func TestThreadStreamReturns404ForAnUnknownThread(t *testing.T) {
	memory := newTestMemoryStore(t)
	hub := webchat.NewHub()
	handler := newThreadStreamHandler(hub, memory)

	request := withPathValue(httptest.NewRequest(http.MethodGet, "/api/chat/threads/missing/stream", nil), "id", "missing")
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestChatApproveEnqueuesAnApprovalDecisionEventWithTheOwnerSet(t *testing.T) {
	var got events.Event
	enqueue := func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}
	handler := newChatApproveHandler(enqueue, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approval_id":"approval-1","approved":true}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.Type != events.TypeApproval || got.Owner != "owner-42" {
		t.Fatalf("event=%#v", got)
	}
	var decision events.ApprovalDecision
	if err := json.Unmarshal(got.Payload, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.ApprovalID != "approval-1" || !decision.Approved {
		t.Fatalf("decision=%#v", decision)
	}
	if decision.CallbackQueryID != "" || decision.MessageID != "" {
		t.Fatalf("expected empty Telegram-only fields, decision=%#v", decision)
	}
}

func TestChatApproveRejectsMissingApprovalID(t *testing.T) {
	handler := newChatApproveHandler(func(context.Context, events.Event) error { return nil }, "owner-42")

	request := httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approved":true}`))
	response := httptest.NewRecorder()
	handler(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}
