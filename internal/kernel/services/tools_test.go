package services

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
	tool := NewStatusTool(store)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Repositories     []string `json:"repositories"`
		PendingApprovals int      `json:"pending_approvals"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Repositories) != 1 || status.Repositories[0] != "eggy" || status.PendingApprovals != 1 {
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
