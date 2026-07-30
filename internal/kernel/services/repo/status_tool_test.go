package repo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestStatusToolReturnsBoundedOperationalView(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	store.state.Approvals["a"] = approvals.Approval{ID: "a", Status: approvals.Pending}
	tool := NewStatusTool(store, stubSchedules{{ID: "morning"}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Repositories     []string `json:"repositories"`
		PendingApprovals int      `json:"pending_approvals"`
		Schedules        int      `json:"schedules"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.Schedules != 1 {
		t.Fatalf("schedules=%d, want the count from the cron directory", status.Schedules)
	}
	if len(status.Repositories) != 1 || status.Repositories[0] != "eggy" || status.PendingApprovals != 1 {
		t.Fatalf("status=%s", result)
	}
}

// stubSchedules stands in for the cron-backed scheduler.
type stubSchedules []ports.Schedule

func (s stubSchedules) List(context.Context) ([]ports.Schedule, error) { return s, nil }
