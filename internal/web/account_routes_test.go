package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/webchat"
)

// fakeAccountApprovals answers Pending from the principal, the way the real
// service does through the per-account store.
type fakeAccountApprovals struct {
	byAccount map[string][]approvals.Approval
}

func (f fakeAccountApprovals) Pending(ctx context.Context) ([]approvals.Approval, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return f.byAccount[principal.AccountID], nil
}

// The route matrix: one account's session against another account's
// thread, history, stream, approvals, traces and schedules. Every private
// resource answers as if it did not exist -- no metadata, no different
// status for "exists but not yours" -- and nothing the browser sends in a
// body can name a different account.
func TestAccountRoutesNeverCrossAccounts(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	memory := newTestMemoryStore(t)
	cfg.Threads, cfg.Memory, cfg.Traces, cfg.Schedules = memory, memory, memory, nil
	cfg.ChatHub = webchat.NewHub()
	cfg.Approvals = fakeAccountApprovals{byAccount: map[string][]approvals.Approval{"nigel": {{ID: "ap-nigel", Status: approvals.Pending, Summary: "nigel's approval", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}}}
	var enqueued []events.Event
	cfg.Enqueue = func(_ context.Context, event events.Event) error { enqueued = append(enqueued, event); return nil }
	handler := NewWebHandler("", cfg)

	nigel := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"})
	if _, err := memory.CreateThread(nigel, "thread-nigel", "web", now); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteMessage(nigel, ports.StoredMessage{ConversationID: "thread-nigel", Role: ports.RoleUser, Content: "nigel's private words", Source: "web", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := memory.StartTrace(nigel, ports.Trace{ID: "trace-nigel", ConversationID: "thread-nigel", Channel: "web", Source: "web", Kind: "owner", Input: "nigel's prompt", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	partnerCookie, partnerCSRF := signIn(t, handler, database, "partner", now)
	do := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		request.AddCookie(partnerCookie)
		request.Header.Set(csrfHeader, partnerCSRF)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, probe := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/chat/threads/thread-nigel/history", ""},
		{http.MethodPatch, "/api/chat/threads/thread-nigel", `{"title":"hijacked"}`},
		{http.MethodDelete, "/api/chat/threads/thread-nigel", ""},
		{http.MethodPost, "/api/chat/threads/thread-nigel/send", `{"text":"hello","owner":"nigel","account_id":"nigel"}`},
		{http.MethodGet, "/api/traces/trace-nigel", ""},
	} {
		response := do(probe.method, probe.target, probe.body)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s: status=%d body=%s", probe.method, probe.target, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "nigel's") {
			t.Errorf("%s %s leaked nigel's content: %s", probe.method, probe.target, response.Body.String())
		}
	}
	for _, target := range []string{"/api/chat/threads", "/api/traces", "/api/approvals"} {
		response := do(http.MethodGet, target, "")
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "nigel") {
			t.Errorf("GET %s: status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
	// A forged owner in a message body cannot enqueue as nigel: the event's
	// owner is the session's account.
	if _, err := memory.CreateThread(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"}), "thread-partner", "web", now); err != nil {
		t.Fatal(err)
	}
	if response := do(http.MethodPost, "/api/chat/threads/thread-partner/send", `{"text":"hi","owner":"nigel"}`); response.Code != http.StatusAccepted {
		t.Fatalf("own send status=%d body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/api/chat/approve", `{"approval_id":"ap-nigel","approved":true,"owner":"nigel"}`); response.Code != http.StatusAccepted {
		t.Fatalf("approve status=%d", response.Code)
	}
	for _, event := range enqueued {
		if event.Owner != "partner" {
			t.Fatalf("event enqueued as %q", event.Owner)
		}
	}
	// nigel's thread is untouched by any of the above.
	if thread, found, _ := memory.GetThread(nigel, "thread-nigel"); !found || thread.Title != "" {
		t.Fatalf("nigel's thread after partner's probes: found=%v thread=%+v", found, thread)
	}
}

func TestStreamRegistersUnderTheSessionAccountAndEndsOnRevocation(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	memory := newTestMemoryStore(t)
	hub := webchat.NewHub()
	cfg.Threads, cfg.Memory, cfg.ChatHub = memory, memory, hub
	handler := NewWebHandler("", cfg)
	partner := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	if _, err := memory.CreateThread(partner, "thread-partner", "web", now); err != nil {
		t.Fatal(err)
	}
	cookie, _ := signIn(t, handler, database, "partner", now)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-partner/stream", nil).WithContext(ctx)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	// Broadcasts for the same thread ID under another account do not reach
	// this stream; the account's own do.
	hub.Broadcast("nigel", "thread-partner", webchat.Event{Kind: webchat.EventMessage, ID: "1", Text: "nigel's broadcast"})
	hub.Broadcast("partner", "thread-partner", webchat.Event{Kind: webchat.EventMessage, ID: "2", Text: "partner's broadcast"})
	time.Sleep(30 * time.Millisecond)
	// Revoking the account's sessions closes the stream immediately, well
	// before the request context would have ended it.
	if err := revokeAccountSessions(context.Background(), cfg, "partner"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("stream survived revocation")
	}
	body := response.Body.String()
	if strings.Contains(body, "nigel's broadcast") || !strings.Contains(body, "partner's broadcast") {
		t.Fatalf("body=%q", body)
	}
}
