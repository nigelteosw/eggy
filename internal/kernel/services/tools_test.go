package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

func TestStatusToolReturnsBoundedOperationalView(t *testing.T) {
	store := newMemoryStore()
	store.state.SelectedRepository = "eggy"
	store.state.ConversationSummary = "private long summary"
	store.state.Approvals["a"] = approvals.Approval{ID: "a", Status: approvals.Pending}
	tool := NewStatusTool(store)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Repository       string `json:"repository"`
		PendingApprovals int    `json:"pending_approvals"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.Repository != "eggy" || status.PendingApprovals != 1 {
		t.Fatalf("status=%s", result)
	}
	if string(result) == "" || contains(string(result), "private long summary") {
		t.Fatalf("state snapshot leaked: %s", result)
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
