package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// A store failure must reach the browser as a 500 that does not repeat it: the
// text can name a table or a statement. A refused request keeps its reason.
func TestAgentFailureStatusesDoNotLeakStoreErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"missing run", ports.ErrAgentRunNotFound, http.StatusNotFound},
		{"refused request", fmt.Errorf("%w: unknown repository %q", ports.ErrAgentRunInvalid, "x"), http.StatusBadRequest},
		{"queued run", ports.ErrAgentRunQueued, http.StatusConflict},
		{"store fault", errors.New("UNIQUE constraint failed: agent_runs.id"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeAgentFailure(recorder, c.err)
			if recorder.Code != c.status {
				t.Fatalf("status=%d, want %d", recorder.Code, c.status)
			}
			if c.status == http.StatusInternalServerError && strings.Contains(recorder.Body.String(), "UNIQUE") {
				t.Fatalf("500 repeats the store error: %s", recorder.Body.String())
			}
		})
	}
}

type invalidScheduleDirectory struct{}

func (invalidScheduleDirectory) List(context.Context) ([]ports.Schedule, error) { return nil, nil }
func (invalidScheduleDirectory) Remove(context.Context, string) error {
	return ports.ErrScheduleInvalid
}

func TestScheduleDeleteRefusesAnInvalidIDAsBadRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("DELETE /api/schedules/{id}", newScheduleDeleteHandler(invalidScheduleDirectory{}))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/schedules/Bad%20Id", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", recorder.Code)
	}
}
