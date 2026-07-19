package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestMemoryToolsOnlyExposeControlledOperations(t *testing.T) {
	memory := &fakeMemory{content: "# Eggy Memory\n"}
	registry := NewToolRegistry()
	for _, tool := range []ports.Tool{NewMemoryLoadTool(memory), NewMemoryAppendTool(memory), NewMemoryReplaceTool(memory)} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(NewMemoryLoadTool(memory)); !errors.Is(err, ErrDuplicateTool) {
		t.Fatalf("duplicate error=%v", err)
	}
	tools := registry.Tools()
	if len(tools) != 3 {
		t.Fatalf("tools=%d", len(tools))
	}

	appendTool, _ := registry.Get("memory_append")
	if _, err := appendTool.Execute(context.Background(), json.RawMessage(`{"section":"Preferences","content":"Concise","extra":true}`)); err == nil {
		t.Fatal("extra field accepted")
	}
	if _, err := appendTool.Execute(context.Background(), json.RawMessage(`{"section":"Preferences","content":"Concise"}`)); err != nil {
		t.Fatal(err)
	}
	if memory.appendSection != "Preferences" || memory.appendContent != "Concise" {
		t.Fatalf("append=%#v", memory)
	}

	replaceTool, _ := registry.Get("memory_replace_section")
	if _, err := replaceTool.Execute(context.Background(), json.RawMessage(`{"section":"Preferences","content":"Practical"}`)); err != nil {
		t.Fatal(err)
	}
	if memory.replaceSection != "Preferences" || memory.replaceContent != "Practical" {
		t.Fatalf("replace=%#v", memory)
	}
	if _, ok := registry.Get("memory_overwrite"); ok {
		t.Fatal("uncontrolled overwrite tool exposed")
	}
}

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

type fakeMemory struct{ content, appendSection, appendContent, replaceSection, replaceContent string }

func (m *fakeMemory) Load(context.Context) (string, error) { return m.content, nil }
func (m *fakeMemory) Append(_ context.Context, section, content string) error {
	m.appendSection, m.appendContent = section, content
	return nil
}
func (m *fakeMemory) ReplaceSection(_ context.Context, section, content string) error {
	m.replaceSection, m.replaceContent = section, content
	return nil
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
