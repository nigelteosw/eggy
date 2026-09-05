package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

type recordingImageDownloader struct {
	fileID    string
	size      int64
	mediaType string
	part      ports.ContentPart
	err       error
}

func (d *recordingImageDownloader) DownloadImage(_ context.Context, fileID string, size int64, mediaType string) (ports.ContentPart, error) {
	d.fileID, d.size, d.mediaType = fileID, size, mediaType
	return d.part, d.err
}

func TestWebhookNormalizesPhotoWithCaption(t *testing.T) {
	part := ports.ContentPart{Type: ports.ContentTypeImage, MediaType: "image/jpeg", Data: []byte("jpeg")}
	downloader := &recordingImageDownloader{part: part}
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}, nil).WithImageDownloader(downloader)
	body := `{"update_id":13,"message":{"message_id":5,"from":{"id":42},"chat":{"id":99},"caption":"read this list","photo":[{"file_id":"small","file_size":100},{"file_id":"large","file_size":300}]}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if downloader.fileID != "large" || downloader.size != 300 || downloader.mediaType != "image/jpeg" {
		t.Fatalf("download=%q %d %q", downloader.fileID, downloader.size, downloader.mediaType)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Text != "read this list" || len(message.Parts) != 1 || string(message.Parts[0].Data) != "jpeg" {
		t.Fatalf("message=%#v", message)
	}
}

func TestWebhookNormalizesCaptionlessPhoto(t *testing.T) {
	downloader := &recordingImageDownloader{part: ports.ContentPart{Type: ports.ContentTypeImage, MediaType: "image/jpeg", Data: []byte("jpeg")}}
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).WithImageDownloader(downloader)
	body := `{"update_id":14,"message":{"message_id":5,"from":{"id":42},"chat":{"id":99},"photo":[{"file_id":"photo","file_size":100}]}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	var message events.Message
	_ = json.Unmarshal(got.Payload, &message)
	if response.Code != http.StatusNoContent || message.Text != "Describe this image." {
		t.Fatalf("status=%d message=%#v", response.Code, message)
	}
}

func TestWebhookNormalizesImageDocument(t *testing.T) {
	downloader := &recordingImageDownloader{part: ports.ContentPart{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}}
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).WithImageDownloader(downloader)
	body := `{"update_id":15,"message":{"message_id":5,"from":{"id":42},"chat":{"id":99},"caption":"original","document":{"file_id":"document","file_name":"list.png","mime_type":"image/png","file_size":400}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	var message events.Message
	_ = json.Unmarshal(got.Payload, &message)
	if response.Code != http.StatusNoContent || downloader.fileID != "document" || downloader.mediaType != "image/png" || message.Text != "original" || len(message.Parts) != 1 {
		t.Fatalf("status=%d downloader=%#v message=%#v", response.Code, downloader, message)
	}
}

func TestWebhookRejectsUnsupportedOrFailedImageDocuments(t *testing.T) {
	tests := []struct {
		name        string
		document    string
		downloadErr error
	}{
		{name: "PDF", document: `{"file_id":"pdf","file_name":"list.pdf","mime_type":"application/pdf","file_size":400}`},
		{name: "unknown file", document: `{"file_id":"unknown","file_name":"list.bin","file_size":400}`},
		{name: "download failure", document: `{"file_id":"image","file_name":"list.png","mime_type":"image/png","file_size":400}`, downloadErr: errors.New("download failed")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			downloader := &recordingImageDownloader{err: tc.downloadErr}
			enqueued := false
			handler := NewWebhookHandler(42, "secret", func(context.Context, events.Event) error { enqueued = true; return nil }, nil).WithImageDownloader(downloader)
			body := `{"update_id":16,"message":{"message_id":5,"from":{"id":42},"chat":{"id":99},"document":` + tc.document + `}}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code < 400 || enqueued {
				t.Fatalf("status=%d enqueued=%v", response.Code, enqueued)
			}
		})
	}
}

func TestWebhookVerifiesSecretOwnerAndNormalizesMessage(t *testing.T) {
	var got events.Event
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil)
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
	if got.Destination.Kind != destination.Telegram {
		t.Fatalf("destination=%#v", got.Destination)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil || message.Text != "hello" {
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

type recordingAcknowledger struct{ acked []string }

func (a *recordingAcknowledger) AnswerCallback(_ context.Context, callbackQueryID string) error {
	a.acked = append(a.acked, callbackQueryID)
	return nil
}

func TestWebhookNormalizesApprovalCallback(t *testing.T) {
	var got events.Event
	acknowledger := &recordingAcknowledger{}
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, acknowledger)
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
	if decision.ApprovalID != "abc" || !decision.Approved || decision.MessageID != "123" {
		t.Fatalf("decision=%#v", decision)
	}
	// The tap is acked as the update arrives, not later on the async event
	// path, so the callback query ID never has to travel on the event.
	if len(acknowledger.acked) != 1 || acknowledger.acked[0] != "cb" {
		t.Fatalf("acked=%v", acknowledger.acked)
	}
}

func TestWebhookRoutesSelectionCallbackAsOwnerMessage(t *testing.T) {
	var got events.Event
	var resolved []string
	acknowledger := &recordingAcknowledger{}
	handler := NewWebhookHandler(42, "secret", func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}, acknowledger).WithSelectionResolver(func(callbackData string) (string, bool) {
		resolved = append(resolved, callbackData)
		return "staging", true
	})
	body := `{"update_id":10,"callback_query":{"id":"cb-select","from":{"id":42},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":99}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent || got.Type != events.TypeMessage || got.Owner != "42" {
		t.Fatalf("status=%d event=%#v", response.Code, got)
	}
	var message events.Message
	if err := json.Unmarshal(got.Payload, &message); err != nil || message.Text != "staging" {
		t.Fatalf("payload=%s err=%v", got.Payload, err)
	}
	if len(resolved) != 1 || resolved[0] != "select:opaque:1" {
		t.Fatalf("resolved=%v", resolved)
	}
	if len(acknowledger.acked) != 1 || acknowledger.acked[0] != "cb-select" {
		t.Fatalf("acked=%v", acknowledger.acked)
	}
}

func TestWebhookRejectsNonOwnerSelectionWithoutConsumingIt(t *testing.T) {
	called := false
	handler := NewWebhookHandler(42, "secret", func(context.Context, events.Event) error { return nil }, nil).
		WithSelectionResolver(func(string) (string, bool) {
			called = true
			return "staging", true
		})
	body := `{"update_id":11,"callback_query":{"id":"cb-select","from":{"id":43},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":99}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
	if called {
		t.Fatal("non-owner callback consumed the pending selection")
	}
}

func TestWebhookAcknowledgesConsumedSelectionWithoutEnqueuingAnEvent(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	enqueued := false
	handler := NewWebhookHandler(42, "secret", func(context.Context, events.Event) error {
		enqueued = true
		return nil
	}, acknowledger).WithSelectionResolver(func(string) (string, bool) {
		return "", false
	})
	body := `{"update_id":12,"callback_query":{"id":"duplicate","from":{"id":42},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":99}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent || enqueued {
		t.Fatalf("status=%d enqueued=%v", response.Code, enqueued)
	}
	if len(acknowledger.acked) != 1 || acknowledger.acked[0] != "duplicate" {
		t.Fatalf("acked=%v", acknowledger.acked)
	}
}

// An unauthorized or malformed update must never be acked: acking is only
// for a tap Eggy has actually accepted.
func TestWebhookDoesNotAcknowledgeARejectedCallback(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	handler := NewWebhookHandler(42, "secret", func(context.Context, events.Event) error { return nil }, acknowledger)
	body := `{"update_id":9,"callback_query":{"id":"cb","from":{"id":43},"data":"approval:abc:approve","message":{"message_id":123,"chat":{"id":99}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
	if len(acknowledger.acked) != 0 {
		t.Fatalf("acked a rejected callback: %v", acknowledger.acked)
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
	client := NewClient("https://api.telegram.test", "token", "99", httpClient)
	if err := client.Deliver(context.Background(), `<ready> & "safe"`); err != nil {
		t.Fatal(err)
	}
	approval := approvals.Approval{ID: "id-1", Action: approvals.Action("test_action"), Summary: "Run protected action"}
	if err := client.DeliverApproval(context.Background(), approval); err != nil {
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
