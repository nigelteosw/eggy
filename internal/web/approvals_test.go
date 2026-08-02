package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
