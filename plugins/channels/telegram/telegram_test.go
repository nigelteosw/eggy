package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type recordingFileDownloader struct {
	fileID    string
	size      int64
	mediaType string
	filename  string
	part      ports.ContentPart
	err       error
}

func (d *recordingFileDownloader) DownloadFile(_ context.Context, fileID string, size int64, mediaType, filename string) (ports.ContentPart, error) {
	d.fileID, d.size, d.mediaType, d.filename = fileID, size, mediaType, filename
	return d.part, d.err
}

func TestWebhookNormalizesPhotoWithCaption(t *testing.T) {
	part := ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/jpeg", Data: []byte("jpeg")}
	downloader := &recordingFileDownloader{part: part}
	var got events.Event
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}, nil).WithFileDownloader(downloader)
	body := `{"update_id":13,"message":{"message_id":5,"from":{"id":42},"chat":{"id":42},"caption":"read this list","photo":[{"file_id":"small","file_size":100},{"file_id":"large","file_size":300}]}}`
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
	downloader := &recordingFileDownloader{part: ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/jpeg", Data: []byte("jpeg")}}
	var got events.Event
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).WithFileDownloader(downloader)
	body := `{"update_id":14,"message":{"message_id":5,"from":{"id":42},"chat":{"id":42},"photo":[{"file_id":"photo","file_size":100}]}}`
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

// A PDF document is downloaded under its own name and, with no caption,
// asks the model to read it rather than describe it.
func TestWebhookNormalizesPDFDocument(t *testing.T) {
	downloader := &recordingFileDownloader{part: ports.ContentPart{Type: ports.ModalityFile, MediaType: "application/pdf", Filename: "list.pdf", Data: []byte("%PDF-")}}
	var got events.Event
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).WithFileDownloader(downloader)
	body := `{"update_id":15,"message":{"message_id":5,"from":{"id":42},"chat":{"id":42},"document":{"file_id":"document","file_name":"list.pdf","mime_type":"application/pdf","file_size":400}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	var message events.Message
	_ = json.Unmarshal(got.Payload, &message)
	if response.Code != http.StatusNoContent || downloader.mediaType != "application/pdf" || downloader.filename != "list.pdf" || message.Text != "Read this file." || len(message.Parts) != 1 || message.Parts[0].Type != ports.ModalityFile {
		t.Fatalf("status=%d downloader=%#v message=%#v", response.Code, downloader, message)
	}
}

func TestWebhookNormalizesImageDocument(t *testing.T) {
	downloader := &recordingFileDownloader{part: ports.ContentPart{Type: ports.ModalityImage, MediaType: "image/png", Data: []byte("png")}}
	var got events.Event
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).WithFileDownloader(downloader)
	body := `{"update_id":15,"message":{"message_id":5,"from":{"id":42},"chat":{"id":42},"caption":"original","document":{"file_id":"document","file_name":"list.png","mime_type":"image/png","file_size":400}}}`
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

type recordingReplier struct {
	account string
	text    string
}

func (r *recordingReplier) Deliver(ctx context.Context, text string) error {
	principal, _ := ports.PrincipalFromContext(ctx)
	r.account, r.text = principal.AccountID, text
	return nil
}

// A message Eggy cannot turn into a turn is still a delivered update.
// Telegram retries any non-2xx response and holds every later update behind
// it, so answering 400 to one PDF silenced the bot for that person until the
// retries gave up. The update is acknowledged, dropped, and explained.
func TestWebhookAcknowledgesAndExplainsUnsupportedOrFailedImageDocuments(t *testing.T) {
	tests := []struct {
		name        string
		document    string
		downloadErr error
	}{
		{name: "zip", document: `{"file_id":"zip","file_name":"list.zip","mime_type":"application/zip","file_size":400}`},
		{name: "unknown file", document: `{"file_id":"unknown","file_name":"list.bin","file_size":400}`},
		{name: "download failure", document: `{"file_id":"image","file_name":"list.png","mime_type":"image/png","file_size":400}`, downloadErr: errors.New("download failed")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			downloader := &recordingFileDownloader{err: tc.downloadErr}
			replier := &recordingReplier{}
			enqueued := false
			handler := NewWebhookHandler(SingleOwner(42), "secret", func(context.Context, events.Event) error { enqueued = true; return nil }, nil).WithFileDownloader(downloader).WithReplier(replier)
			body := `{"update_id":16,"message":{"message_id":5,"from":{"id":42},"chat":{"id":42},"document":` + tc.document + `}}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent || enqueued {
				t.Fatalf("status=%d enqueued=%v", response.Code, enqueued)
			}
			if replier.account != "42" || !strings.Contains(replier.text, "Telegram") {
				t.Fatalf("reply account=%q text=%q", replier.account, replier.text)
			}
		})
	}
}

func TestWebhookVerifiesSecretOwnerAndNormalizesMessage(t *testing.T) {
	var got events.Event
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil)
	body := `{"update_id":7,"message":{"message_id":3,"from":{"id":42},"chat":{"id":42},"text":"hello"}}`

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
	if response.Code != http.StatusNoContent {
		t.Fatalf("owner status=%d", response.Code)
	}
}

type recordingAcknowledger struct{ acked []string }

func (a *recordingAcknowledger) AnswerCallback(_ context.Context, callbackQueryID string) error {
	a.acked = append(a.acked, callbackQueryID)
	return nil
}

func TestWebhookCarriesARepliedToMessageAsAQuote(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       *events.Quote
	}{
		{"reply to Eggy",
			`{"update_id":8,"message":{"message_id":4,"from":{"id":42},"chat":{"id":42},"text":"why?","reply_to_message":{"message_id":3,"from":{"id":9,"is_bot":true},"chat":{"id":42},"text":"Because of MAS Notice 643."}}}`,
			&events.Quote{Text: "Because of MAS Notice 643.", OwnMessage: true}},
		{"partial quote wins over the whole message",
			`{"update_id":8,"message":{"message_id":4,"from":{"id":42},"chat":{"id":42},"text":"why?","reply_to_message":{"message_id":3,"from":{"id":9,"is_bot":true},"chat":{"id":42},"text":"A long message."},"quote":{"text":"long"}}}`,
			&events.Quote{Text: "long", OwnMessage: true}},
		{"reply to the owner's own earlier message",
			`{"update_id":8,"message":{"message_id":4,"from":{"id":42},"chat":{"id":42},"text":"this one","reply_to_message":{"message_id":2,"from":{"id":42},"chat":{"id":42},"text":"earlier"}}}`,
			&events.Quote{Text: "earlier"}},
		{"media reply falls back to its caption",
			`{"update_id":8,"message":{"message_id":4,"from":{"id":42},"chat":{"id":42},"text":"what is this","reply_to_message":{"message_id":3,"from":{"id":9,"is_bot":true},"chat":{"id":42},"caption":"chart"}}}`,
			&events.Quote{Text: "chart", OwnMessage: true}},
		{"reply to something with no text is no quote",
			`{"update_id":8,"message":{"message_id":4,"from":{"id":42},"chat":{"id":42},"text":"hm","reply_to_message":{"message_id":3,"from":{"id":9,"is_bot":true},"chat":{"id":42}}}}`,
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got events.Event
			handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var message events.Message
			if err := json.Unmarshal(got.Payload, &message); err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if message.Quote != nil {
					t.Fatalf("quote=%#v, want none", message.Quote)
				}
				return
			}
			if message.Quote == nil || *message.Quote != *tc.want {
				t.Fatalf("quote=%#v, want %#v", message.Quote, tc.want)
			}
		})
	}
}

func TestWebhookNormalizesApprovalCallback(t *testing.T) {
	var got events.Event
	acknowledger := &recordingAcknowledger{}
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, acknowledger)
	body := `{"update_id":8,"callback_query":{"id":"cb","from":{"id":42},"data":"approval:abc:approve","message":{"message_id":123,"chat":{"id":42}}}}`
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
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(_ context.Context, event events.Event) error {
		got = event
		return nil
	}, acknowledger).WithSelectionResolver(func(ctx context.Context, callbackData string) (string, bool) {
		principal, err := ports.PrincipalFromContext(ctx)
		if err != nil || principal.AccountID != "42" {
			t.Fatalf("resolver identity=%v err=%v", principal, err)
		}
		resolved = append(resolved, callbackData)
		return "staging", true
	})
	body := `{"update_id":10,"callback_query":{"id":"cb-select","from":{"id":42},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":42}}}}`
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
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(context.Context, events.Event) error { return nil }, nil).
		WithSelectionResolver(func(context.Context, string) (string, bool) {
			called = true
			return "staging", true
		})
	body := `{"update_id":11,"callback_query":{"id":"cb-select","from":{"id":43},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":42}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}
	if called {
		t.Fatal("non-owner callback consumed the pending selection")
	}
}

func TestWebhookAcknowledgesConsumedSelectionWithoutEnqueuingAnEvent(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	enqueued := false
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(context.Context, events.Event) error {
		enqueued = true
		return nil
	}, acknowledger).WithSelectionResolver(func(context.Context, string) (string, bool) {
		return "", false
	})
	body := `{"update_id":12,"callback_query":{"id":"duplicate","from":{"id":42},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":42}}}}`
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
	handler := NewWebhookHandler(SingleOwner(42), "secret", func(context.Context, events.Event) error { return nil }, acknowledger)
	body := `{"update_id":9,"callback_query":{"id":"cb","from":{"id":43},"data":"approval:abc:approve","message":{"message_id":123,"chat":{"id":42}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
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
	client := NewClient("https://api.telegram.test", "token", FixedChat("99"), httpClient)
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

// A sender is mapped to an account by number, never by name, and only in a
// private chat: a group message from the same person is refused, because a
// reply would land where other people read it.
func TestWebhookMapsSendersToAccountsAndRefusesGroupsAndStrangers(t *testing.T) {
	var got events.Event
	resolve := func(sender int64) (string, bool) {
		switch sender {
		case 42:
			return "nigel", true
		case 77:
			return "partner", true
		}
		return "", false
	}
	handler := NewWebhookHandler(resolve, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil)
	post := func(body string) int {
		request := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", strings.NewReader(body))
		request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if code := post(`{"update_id":1,"message":{"message_id":1,"from":{"id":77},"chat":{"id":77},"text":"hi"}}`); code != http.StatusNoContent || got.Owner != "partner" {
		t.Fatalf("mapped sender: status=%d owner=%q", code, got.Owner)
	}
	got = events.Event{}
	if code := post(`{"update_id":2,"message":{"message_id":1,"from":{"id":5},"chat":{"id":5},"text":"hi"}}`); code != http.StatusNoContent || got.Owner != "" {
		t.Fatalf("stranger: status=%d owner=%q", code, got.Owner)
	}
	if code := post(`{"update_id":3,"message":{"message_id":1,"from":{"id":42},"chat":{"id":-100123},"text":"hi"}}`); code != http.StatusNoContent || got.Owner != "" {
		t.Fatalf("group: status=%d owner=%q", code, got.Owner)
	}
	if code := post(`{"update_id":4,"callback_query":{"id":"cb","from":{"id":42},"data":"approval:a1:approve","message":{"message_id":9,"chat":{"id":-100123}}}}`); code != http.StatusNoContent || got.Owner != "" {
		t.Fatalf("group callback: status=%d owner=%q", code, got.Owner)
	}
}

func TestWebhookAllowsOnlyExactPrivatePairingForUnmappedSender(t *testing.T) {
	var payload string
	var sender int64
	enqueued := false
	handler := NewWebhookHandler(func(int64) (string, bool) { return "", false }, "secret", func(context.Context, events.Event) error {
		enqueued = true
		return nil
	}, nil).WithPairingConsumer(func(_ context.Context, code string, userID int64) error {
		payload, sender = code, userID
		return nil
	})
	post := func(chat int64, text string) int {
		body := fmt.Sprintf(`{"update_id":1,"message":{"message_id":1,"from":{"id":77},"chat":{"id":%d},"text":%q}}`, chat, text)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}
	if code := post(77, "/start opaque-code"); code != http.StatusNoContent || payload != "opaque-code" || sender != 77 || enqueued {
		t.Fatalf("pairing status=%d payload=%q sender=%d enqueued=%v", code, payload, sender, enqueued)
	}
	for _, attempt := range []struct {
		chat int64
		text string
	}{{77, "hello"}, {77, "/start"}, {77, "/start a b"}, {-100, "/start other"}} {
		payload = ""
		if code := post(attempt.chat, attempt.text); code != http.StatusNoContent || payload != "" {
			t.Fatalf("attempt=%+v status=%d payload=%q", attempt, code, payload)
		}
	}
}

// Delivery goes to the acting account's chat and nowhere else: an account
// without a Telegram chat gets an error, not the first configured chat.
func TestClientDeliversToTheActingAccountsChatOnly(t *testing.T) {
	var chats []string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		chats = append(chats, fmt.Sprint(payload["chat_id"]))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1}}`))}, nil
	})}
	client := NewClient("https://api.telegram.test", "token", func(account string) (string, bool) {
		if account == "nigel" {
			return "42", true
		}
		return "", false
	}, httpClient)
	nigel := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"})
	if err := client.Deliver(nigel, "hello"); err != nil || len(chats) != 1 || chats[0] != "42" {
		t.Fatalf("chats=%v err=%v", chats, err)
	}
	webOnly := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	if err := client.Deliver(webOnly, "hello"); !errors.Is(err, ErrNoRecipient) || len(chats) != 1 {
		t.Fatalf("web-only account: chats=%v err=%v", chats, err)
	}
	if err := client.SendTyping(context.Background()); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("no principal: err=%v", err)
	}
}

// The verified sender rides on the event only for a message typed in the
// private chat: a selection callback carries none, even when its selected
// text is a command, and nothing is stamped for a refused update.
func TestWebhookStampsTheVerifiedSenderOnMessagesOnly(t *testing.T) {
	var got events.Event
	resolve := func(sender int64) (string, bool) {
		if sender == 77 {
			return "partner", true
		}
		return "", false
	}
	handler := NewWebhookHandler(resolve, "secret", func(_ context.Context, event events.Event) error { got = event; return nil }, nil).
		WithSelectionResolver(func(context.Context, string) (string, bool) { return "ordinary selection", true })
	post := func(body, secret string) int {
		request := httptest.NewRequest(http.MethodPost, "/webhooks/telegram", strings.NewReader(body))
		request.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if code := post(`{"update_id":1,"message":{"message_id":1,"from":{"id":77},"chat":{"id":77},"text":"/web"}}`, "secret"); code != http.StatusNoContent || got.SenderID != "77" || got.Owner != "partner" {
		t.Fatalf("private message: status=%d sender=%q owner=%q", code, got.SenderID, got.Owner)
	}
	got = events.Event{}
	if code := post(`{"update_id":2,"callback_query":{"id":"cb","from":{"id":77},"data":"select:abc","message":{"message_id":9,"chat":{"id":77}}}}`, "secret"); code != http.StatusNoContent || got.SenderID != "" || got.Owner != "partner" {
		t.Fatalf("selection callback: status=%d sender=%q owner=%q", code, got.SenderID, got.Owner)
	}
	got = events.Event{}
	if code := post(`{"update_id":3,"message":{"message_id":1,"from":{"id":77},"chat":{"id":77},"text":"/web"}}`, "wrong"); code != http.StatusUnauthorized || got.SenderID != "" {
		t.Fatalf("bad secret: status=%d sender=%q", code, got.SenderID)
	}
	if code := post(`{"update_id":4,"message":{"message_id":1,"from":{"id":5},"chat":{"id":5},"text":"/web"}}`, "secret"); code != http.StatusNoContent || got.SenderID != "" {
		t.Fatalf("unmapped sender: status=%d sender=%q", code, got.SenderID)
	}
	if code := post(`{"update_id":5,"message":{"message_id":1,"from":{"id":77},"chat":{"id":-100123},"text":"/web"}}`, "secret"); code != http.StatusNoContent || got.SenderID != "" {
		t.Fatalf("group: status=%d sender=%q", code, got.SenderID)
	}
}

func TestWebhookSelectionCannotDispatchCommands(t *testing.T) {
	for _, value := range []string{"/mode auto", " /restart", "/clear", "/web", "/mcp remove server", "/google logout", "/model add bad provider model"} {
		t.Run(value, func(t *testing.T) {
			calls := 0
			handler := NewWebhookHandler(SingleOwner(42), "secret", func(context.Context, events.Event) error { calls++; return nil }, nil).
				WithSelectionResolver(func(context.Context, string) (string, bool) { return value, true })
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"update_id":10,"callback_query":{"id":"cb","from":{"id":42},"data":"select:opaque:1","message":{"message_id":124,"chat":{"id":42}}}}`))
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent || calls != 0 {
				t.Fatalf("status=%d dispatched=%d", response.Code, calls)
			}
		})
	}
}
