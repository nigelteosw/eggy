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

// TestHandleApprovalDeliversFailureMessageWhenExecutionFails covers the gap
// that made a failed approve tap look identical to a broken button: before
// this, handleApproval returned any Decide/executor-lookup/ExecuteApproved
// error straight to Run's event-loop goroutine, which only logs it -- the
// owner who tapped Approve saw nothing at all, with no way to tell a bug
// apart from an ordinary provider failure (e.g. an expired Calendar OAuth
// token). Approving a CalendarCreate here with no calendar executor
// registered (appTestConfig leaves Calendar disabled) reproduces that
// "unknown approval action" failure path and asserts it now reaches the
// owner.
func TestHandleApprovalDeliversFailureMessageWhenExecutionFails(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	var delivered []byte
	client := &http.Client{Transport: appRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "editMessageText"):
			var payload map[string]any
			body, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(body, &payload)
			delivered, _ = json.Marshal(payload)
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
	payload, _ := json.Marshal(events.ApprovalDecision{ApprovalID: approval.ID, Approved: true, CallbackQueryID: "cb-1", MessageID: "777"})
	if err := app.HandleEvent(context.Background(), events.Event{ID: "decision-1", Type: events.TypeApproval, Owner: "42", Payload: payload}); err == nil {
		t.Fatal("expected HandleEvent to still surface the underlying error")
	}
	if !strings.Contains(string(delivered), "Action failed") {
		t.Fatalf("expected a delivered failure message, got %q", delivered)
	}
}

// TestInvalidateStaleShippingApprovalsClearsOnlyLeftoverShippingActions
// covers the P0 cleanup step: ShippingService.Ship now issues, decides, and
// authorizes its own commit/push/pull-request approvals in one call, so a
// pending approval for one of those three actions can only be a leftover
// from before that change -- it must be invalidated at startup rather than
// sit waiting for a Telegram tap that will never come. A pending approval
// for an action still on the human-tap path (e.g. CalendarCreate) must be
// left untouched.
func TestInvalidateStaleShippingApprovalsClearsOnlyLeftoverShippingActions(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := app.approvals.Request(context.Background(), approvals.Commit, map[string]string{"run_id": "run-1"}, "Commit changes")
	if err != nil {
		t.Fatal(err)
	}
	calendar, err := app.approvals.Request(context.Background(), approvals.CalendarCreate, map[string]string{"id": "evt-1"}, "Create event")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.invalidateStaleShippingApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := app.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Approvals[commit.ID].Status; got != approvals.Invalidated {
		t.Fatalf("stale commit approval status=%q, want %q", got, approvals.Invalidated)
	}
	if got := state.Approvals[calendar.ID].Status; got != approvals.Pending {
		t.Fatalf("calendar approval status=%q, want still %q", got, approvals.Pending)
	}
}
