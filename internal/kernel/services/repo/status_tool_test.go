package repo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// The count alone left the agent telling the owner "there is one pending
// approval" and, when asked what for, guessing. Status names each one.
func TestStatusToolNamesWhatEachPendingApprovalIsFor(t *testing.T) {
	store := newMemoryStore()
	older := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	store.state.Approvals["zoo"] = approvals.Approval{
		ID: "zoo", Action: "calendar_create", Summary: "Create [L+N] Zoo on Sat 25 July",
		Status: approvals.Pending, CreatedAt: older.Add(time.Hour), ExpiresAt: older.Add(48 * time.Hour),
		Payload: json.RawMessage(`{"title":"[L+N] Zoo"}`),
	}
	store.state.Approvals["dinner"] = approvals.Approval{
		ID: "dinner", Action: "calendar_create", Summary: "Create dinner on Fri",
		Status: approvals.Pending, CreatedAt: older,
	}
	store.state.Approvals["done"] = approvals.Approval{ID: "done", Status: approvals.Approved, Summary: "already decided"}

	result, err := NewStatusTool(store, stubSchedules{}).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		PendingApprovals int `json:"pending_approvals"`
		Approvals        []struct {
			ID      string `json:"id"`
			Action  string `json:"action"`
			Summary string `json:"summary"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.PendingApprovals != 2 || len(status.Approvals) != 2 {
		t.Fatalf("status=%s, want only the two pending approvals", result)
	}
	// Oldest first, so "what is still sitting there" reads in the order it
	// arrived rather than in map order.
	if status.Approvals[0].ID != "dinner" || status.Approvals[1].Summary != "Create [L+N] Zoo on Sat 25 July" {
		t.Fatalf("approvals=%#v", status.Approvals)
	}
	if status.Approvals[1].Action != "calendar_create" {
		t.Fatalf("action lost: %#v", status.Approvals[1])
	}
	// The payload stays out: it is unbounded and not the agent's to relay.
	if strings.Contains(string(result), `"payload"`) {
		t.Fatalf("status=%s, want the payload withheld", result)
	}
}

// stubSchedules stands in for the cron-backed scheduler.
type stubSchedules []ports.Schedule

func (s stubSchedules) List(context.Context) ([]ports.Schedule, error) { return s, nil }
