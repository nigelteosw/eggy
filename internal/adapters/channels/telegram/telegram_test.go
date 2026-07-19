package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

func TestWebhookVerifiesSecretOwnerAndNormalizesMessage(t *testing.T) {
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil })
	body := `{"update_id":7,"message":{"message_id":3,"from":{"id":42},"chat":{"id":99},"text":"hello"}}`

	for _, tc := range []struct {
		name, secret string
		want         int
	}{{"missing", "", http.StatusUnauthorized}, {"wrong", "bad", http.StatusUnauthorized}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", tc.secret)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.ID != "telegram:7" || got.Owner != "42" || got.Type != events.TypeMessage {
		t.Fatalf("event=%#v", got)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil || message.ChatID != "99" || message.Text != "hello" {
		t.Fatalf("payload=%s err=%v", got.Payload, err)
	}

	denied := strings.Replace(body, `"id":42`, `"id":43`, 1)
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(denied))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("owner status=%d", response.Code)
	}
}

func TestWebhookNormalizesApprovalCallback(t *testing.T) {
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil })
	body := `{"update_id":8,"callback_query":{"id":"cb","from":{"id":42},"data":"approval:abc:approve","message":{"message_id":123,"chat":{"id":99}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || got.Type != events.TypeApproval {
		t.Fatalf("status=%d event=%#v", response.Code, got)
	}
	var decision events.ApprovalDecision
	_ = json.Unmarshal(got.Payload, &decision)
	if decision.ApprovalID != "abc" || !decision.Approved || decision.CallbackQueryID != "cb" || decision.MessageID != "123" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestClientSendsTextAndApprovalKeyboard(t *testing.T) {
	var requests []map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		requests = append(requests, payload)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", httpClient)
	if err := client.Deliver(context.Background(), "99", `<ready> & "safe"`); err != nil {
		t.Fatal(err)
	}
	approval := approvals.Approval{ID: "id-1", Action: approvals.Commit, Summary: "Commit changes"}
	if err := client.DeliverApproval(context.Background(), "99", approval); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0]["parse_mode"] != "HTML" || requests[0]["text"] != `&lt;ready&gt; &amp; "safe"` {
		t.Fatalf("requests=%#v", requests)
	}
	preview, ok := requests[0]["link_preview_options"].(map[string]any)
	if !ok || preview["is_disabled"] != true {
		t.Fatalf("ordinary delivery did not disable Telegram link previews: %#v", requests[0])
	}
	markup := requests[1]["reply_markup"].(map[string]any)
	if markup["inline_keyboard"] == nil {
		t.Fatalf("missing keyboard: %#v", requests[1])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
