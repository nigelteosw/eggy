package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
)

func TestHandleApprovalAnswersCallbackAndEditsRejectionMessageInPlace(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var editRequests []map[string]any
	var answerRequests []map[string]any
	var sendMessageCount int
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "editMessageText"):
			var payload map[string]any
			body, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(body, &payload)
			editRequests = append(editRequests, payload)
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		case strings.Contains(request.URL.Path, "answerCallbackQuery"):
			var payload map[string]any
			body, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(body, &payload)
			answerRequests = append(answerRequests, payload)
			return appJSON(200, `{"ok":true,"result":true}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			sendMessageCount++
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		default:
			return appJSON(200, `{"ok":true,"result":true}`), nil
		}
	})}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := app.approvals.Request(context.Background(), approvals.CalendarCreate, map[string]string{"id": "evt-1"}, "Create event")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approval.ID, Approved: false, CallbackQueryID: "cb-1", MessageID: "777"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decision-1", Type: events.TypeApproval, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(answerRequests) != 1 || answerRequests[0]["callback_query_id"] != "cb-1" {
		t.Fatalf("answer=%#v", answerRequests)
	}
	if len(editRequests) != 1 || editRequests[0]["chat_id"] != "42" || editRequests[0]["message_id"] != "777" {
		t.Fatalf("edit=%#v", editRequests)
	}
	if sendMessageCount != 0 {
		t.Fatalf("expected no fallback sendMessage, got %d", sendMessageCount)
	}
}

func TestHandleApprovalFallsBackToNewMessageWhenEditFails(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var delivered []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "editMessageText"):
			return appJSON(200, `{"ok":false,"description":"Bad Request: message to edit not found"}`), nil
		case strings.Contains(request.URL.Path, "sendMessage"):
			delivered, _ = io.ReadAll(request.Body)
			return appJSON(200, `{"ok":true,"result":{}}`), nil
		default:
			return appJSON(200, `{"ok":true,"result":true}`), nil
		}
	})}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{HTTPClient: client, TelegramBaseURL: "https://telegram.test", ProviderBaseURLs: map[string]string{"deepseek": "https://deepseek.test"}})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := app.approvals.Request(context.Background(), approvals.CalendarCreate, map[string]string{"id": "evt-1"}, "Create event")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approval.ID, Approved: false, CallbackQueryID: "cb-1", MessageID: "777"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decision-1", Type: events.TypeApproval, Owner: "42", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(delivered), "Action rejected.") {
		t.Fatalf("expected fallback delivery of rejection message, got %q", delivered)
	}
}
