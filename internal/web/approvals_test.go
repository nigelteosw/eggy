package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

type fakeApprovalDirectory struct{ pending []approvals.Approval }

func (f *fakeApprovalDirectory) Pending(context.Context) ([]approvals.Approval, error) {
	return append([]approvals.Approval(nil), f.pending...), nil
}

// "1 approval waiting" was the whole answer an owner could get, with nothing
// able to say what it was. This is the route that makes the count legible.
func TestWebApprovalsListReportsWhatIsWaiting(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	directory := &fakeApprovalDirectory{pending: []approvals.Approval{
		{ID: "live", Action: "calendar.delete", Summary: "Delete the 3pm sync", Status: approvals.Pending,
			CreatedAt: now.Add(-5 * time.Minute), ExpiresAt: now.Add(25 * time.Minute)},
		{ID: "stale", Action: "calendar.create", Summary: "Book a room", Status: approvals.Pending,
			CreatedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-150 * time.Minute)},
	}}
	webConfig := testWebConfig(now)
	webConfig.Approvals = directory
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/approvals", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded webResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 2 {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
	if decoded.TableRows[0][0] != "live" || decoded.TableRows[0][2] != "Delete the 3pm sync" {
		t.Fatalf("row=%#v", decoded.TableRows[0])
	}
	if decoded.TableRows[0][3] != "waiting" {
		t.Fatalf("state=%q, want waiting", decoded.TableRows[0][3])
	}
	// An approval past its window still counts as pending in state, so it is
	// reported as expired rather than hidden -- otherwise the count and the
	// list disagree, which is the confusion this page exists to end.
	if decoded.TableRows[1][3] != "expired" {
		t.Fatalf("state=%q, want expired", decoded.TableRows[1][3])
	}
}

func TestWebApprovalsRouteRequiresSession(t *testing.T) {
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Approvals = &fakeApprovalDirectory{}
	handler := NewWebHandler("", webConfig)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/approvals", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

type fakeAutoMode struct{ auto bool }

func (f *fakeAutoMode) AutoApprove(context.Context) (bool, error) { return f.auto, nil }

func (f *fakeAutoMode) SetAutoApprove(_ context.Context, auto bool) error {
	f.auto = auto
	return nil
}

// The panel and /auto are one switch. POST toggles rather than setting a
// value, so neither surface can ask for a state the other cannot express, and
// the reported wording is the same one Telegram sends.
func TestWebAutoModeTogglesTheSameSwitch(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	gate := &fakeAutoMode{}
	webConfig := testWebConfig(now)
	webConfig.AutoMode = gate
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	read := func(method, path string) webResult {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var decoded webResult
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	if got := read(http.MethodGet, "/api/approvals/auto"); got.Title != commands.AutoModeMessage(false) {
		t.Fatalf("initial read did not report the gate as on: %+v", got)
	}
	enabled := read(http.MethodPost, "/api/approvals/auto")
	if !gate.auto || enabled.Title != commands.AutoModeMessage(true) {
		t.Fatalf("toggle did not enable auto mode: auto=%v result=%+v", gate.auto, enabled)
	}
	// The state travels as a field, not only as prose, so the switch renders
	// from the server's answer rather than from what the click assumed.
	if len(enabled.Fields) != 1 || enabled.Fields[0].Label != "auto_mode" || enabled.Fields[0].Value != "enabled" {
		t.Fatalf("toggle did not report machine-readable state: %+v", enabled.Fields)
	}
	if disabled := read(http.MethodPost, "/api/approvals/auto"); gate.auto || disabled.Fields[0].Value != "disabled" {
		t.Fatalf("second toggle did not switch back: auto=%v result=%+v", gate.auto, disabled)
	}
}

// An unauthenticated toggle would be a bypass anyone could flip.
func TestWebAutoModeRequiresASession(t *testing.T) {
	webConfig := testWebConfig(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	webConfig.AutoMode = &fakeAutoMode{}
	handler := NewWebHandler("", webConfig)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/approvals/auto", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
