package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
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

type fakeApprovalMode struct{ mode ports.ApprovalMode }

func (f *fakeApprovalMode) Mode(context.Context) (ports.ApprovalMode, error) {
	if f.mode == "" {
		return ports.ModeNormal, nil
	}
	return f.mode, nil
}

func (f *fakeApprovalMode) SetMode(_ context.Context, mode ports.ApprovalMode) error {
	f.mode = mode
	return nil
}

// The panel and /mode are one switch. POST names the mode it wants rather than
// advancing to the next one, so a panel and a phone starting from different
// states cannot disagree about where they ended up, and the reported wording
// is the same one Telegram sends.
func TestWebApprovalModeSetsTheSameSwitch(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	gate := &fakeApprovalMode{}
	webConfig := testWebConfig(now)
	webConfig.ApprovalMode = gate
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)

	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var request *http.Request
		if body == "" {
			request = httptest.NewRequest(method, path, nil)
		} else {
			request = httptest.NewRequest(method, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	decode := func(response *httptest.ResponseRecorder) webResult {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var decoded webResult
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	if got := decode(call(http.MethodGet, "/api/approvals/mode", "")); got.Title != commands.ModeMessage(ports.ModeNormal) {
		t.Fatalf("initial read did not report the default mode: %+v", got)
	}
	strict := decode(call(http.MethodPost, "/api/approvals/mode", "mode=strict"))
	if gate.mode != ports.ModeStrict || strict.Title != commands.ModeMessage(ports.ModeStrict) {
		t.Fatalf("mode=%q result=%+v", gate.mode, strict)
	}
	// The state travels as a field, not only as prose, so the panel renders
	// from the server's answer rather than from what the click assumed.
	if len(strict.Fields) != 1 || strict.Fields[0].Label != "approval_mode" || strict.Fields[0].Value != "strict" {
		t.Fatalf("no machine-readable state: %+v", strict.Fields)
	}
	// An unknown mode is refused rather than resolved to a working one.
	if response := call(http.MethodPost, "/api/approvals/mode", "mode=readonly"); response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
	if gate.mode != ports.ModeStrict {
		t.Fatalf("a refused mode still changed the switch: %q", gate.mode)
	}
}

// An unauthenticated write would let anyone who can reach the port turn every
// gate off.
func TestWebApprovalModeRequiresASession(t *testing.T) {
	webConfig := testWebConfig(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	webConfig.ApprovalMode = &fakeApprovalMode{}
	handler := NewWebHandler("", webConfig)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/approvals/mode", strings.NewReader("mode=auto")))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
