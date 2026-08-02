package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeScheduleDirectory struct {
	schedules []ports.Schedule
	removed   []string
}

func (f *fakeScheduleDirectory) List(context.Context) ([]ports.Schedule, error) {
	return append([]ports.Schedule(nil), f.schedules...), nil
}

func (f *fakeScheduleDirectory) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

func scheduleTestHandler(t *testing.T, directory ScheduleDirectory) (http.Handler, *http.Cookie) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	webConfig := testWebConfig(now)
	webConfig.Schedules = directory
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

// The status tool could only ever report a count, so "4 schedules" was the
// whole answer an owner could get. This is the route that makes the count
// inspectable.
func TestWebSchedulesListReadsAsATimeline(t *testing.T) {
	directory := &fakeScheduleDirectory{schedules: []ports.Schedule{
		{ID: "b", Kind: ports.ScheduleRecurring, Execution: ports.ScheduleExecutionAgent, Instruction: "check the deploy", Expression: "0 9 * * *", NextRun: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC), Enabled: true},
		{ID: "a", Kind: ports.ScheduleExact, Execution: ports.ScheduleExecutionMessage, Instruction: "stand up", NextRun: time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC), Enabled: true},
	}}
	handler, cookie := scheduleTestHandler(t, directory)

	request := httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
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
	// Soonest first, and id first in the row so the delete button has a key.
	if decoded.TableRows[0][0] != "a" {
		t.Fatalf("order=%#v, want the soonest next run first", decoded.TableRows)
	}
	// A reminder says so: "exact reminder" is what the owner thinks they
	// made, where "exact" plus a separate "message" column is the storage
	// shape leaking out.
	if decoded.TableRows[0][1] != "exact reminder" {
		t.Fatalf("kind=%q", decoded.TableRows[0][1])
	}
	if decoded.TableRows[1][3] != "0 9 * * *" {
		t.Fatalf("expression=%q", decoded.TableRows[1][3])
	}
}

func TestWebScheduleDeleteCancelsIt(t *testing.T) {
	directory := &fakeScheduleDirectory{}
	handler, cookie := scheduleTestHandler(t, directory)

	request := httptest.NewRequest(http.MethodDelete, "/api/schedules/sched-1", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(directory.removed) != 1 || directory.removed[0] != "sched-1" {
		t.Fatalf("removed=%#v", directory.removed)
	}
}

func TestWebScheduleRoutesRequireSession(t *testing.T) {
	handler, _ := scheduleTestHandler(t, &fakeScheduleDirectory{})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/schedules", nil),
		httptest.NewRequest(http.MethodDelete, "/api/schedules/sched-1", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", request.URL.Path, response.Code)
		}
	}
}

// A deployment with no scheduler wired answers an empty list rather than
// failing, the same way the status tool treats an unreadable cron directory
// as zero rather than denying every other fact.
func TestWebSchedulesWithoutASchedulerListsNothing(t *testing.T) {
	handler, cookie := scheduleTestHandler(t, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
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
	if len(decoded.TableRows) != 0 {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}
