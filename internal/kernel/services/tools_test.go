package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestStatusToolReturnsBoundedOperationalView(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	store.state.Approvals["a"] = approvals.Approval{ID: "a", Status: approvals.Pending}
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run-1"] = ports.ImplementationSession{ID: "run-1", Phase: ports.PhaseRunning}
	sessionStore.sessions["run-2"] = ports.ImplementationSession{ID: "run-2", Phase: ports.PhaseCompleted}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	tool := NewStatusTool(store, sessions)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Repositories     []string `json:"repositories"`
		ActiveRuns       int      `json:"active_runs"`
		PendingApprovals int      `json:"pending_approvals"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Repositories) != 1 || status.Repositories[0] != "eggy" || status.PendingApprovals != 1 || status.ActiveRuns != 1 {
		t.Fatalf("status=%s", result)
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
