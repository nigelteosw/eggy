package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

var ErrDuplicateTool = errors.New("duplicate tool")

type ToolRegistry struct{ tools map[string]ports.Tool }

func NewToolRegistry() *ToolRegistry { return &ToolRegistry{tools: map[string]ports.Tool{}} }

func (r *ToolRegistry) Register(tool ports.Tool) error {
	name := tool.Definition().Name
	if name == "" {
		return errors.New("tool name is empty")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, name)
	}
	r.tools[name] = tool
	return nil
}

func (r *ToolRegistry) Get(name string) (ports.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) Tools() []ports.Tool {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ports.Tool, 0, len(names))
	for _, name := range names {
		result = append(result, r.tools[name])
	}
	return result
}

type memoryLoadTool struct{ memory ports.MemoryStore }

func NewMemoryLoadTool(memory ports.MemoryStore) ports.Tool { return memoryLoadTool{memory: memory} }
func (t memoryLoadTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "memory_load", Description: "Load durable user-approved memory", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
}
func (t memoryLoadTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeStrict(raw, &struct{}{}); err != nil {
		return nil, err
	}
	content, err := t.memory.Load(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"memory": content})
}

type memoryEditInput struct {
	Section string `json:"section"`
	Content string `json:"content"`
}

type memoryAppendTool struct{ memory ports.MemoryStore }

func NewMemoryAppendTool(memory ports.MemoryStore) ports.Tool {
	return memoryAppendTool{memory: memory}
}
func (t memoryAppendTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "memory_append", Description: "Append durable context to one named memory section", Schema: memoryEditSchema()}
}
func (t memoryAppendTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input memoryEditInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Section == "" || input.Content == "" {
		return nil, errors.New("section and content are required")
	}
	if err := t.memory.Append(ctx, input.Section, input.Content); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"updated":true}`), nil
}

type memoryReplaceTool struct{ memory ports.MemoryStore }

func NewMemoryReplaceTool(memory ports.MemoryStore) ports.Tool {
	return memoryReplaceTool{memory: memory}
}
func (t memoryReplaceTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "memory_replace_section", Description: "Replace one named durable memory section", Schema: memoryEditSchema()}
}
func (t memoryReplaceTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input memoryEditInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Section == "" || input.Content == "" {
		return nil, errors.New("section and content are required")
	}
	if err := t.memory.ReplaceSection(ctx, input.Section, input.Content); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"updated":true}`), nil
}

func memoryEditSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","minLength":1},"content":{"type":"string","minLength":1}},"required":["section","content"],"additionalProperties":false}`)
}

type statusTool struct{ store ports.StateStore }

func NewStatusTool(store ports.StateStore) ports.Tool { return statusTool{store: store} }
func (t statusTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "status", Description: "Read bounded Eggy operational status", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
}
func (t statusTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeStrict(raw, &struct{}{}); err != nil {
		return nil, err
	}
	state, err := t.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	pending := 0
	for _, approval := range state.Approvals {
		if approval.Status == approvals.Pending {
			pending++
		}
	}
	return json.Marshal(struct {
		Repository       string `json:"repository,omitempty"`
		ActiveRuns       int    `json:"active_runs"`
		PendingApprovals int    `json:"pending_approvals"`
		Schedules        int    `json:"schedules"`
	}{Repository: state.SelectedRepository, ActiveRuns: activeRuns(state), PendingApprovals: pending, Schedules: len(state.Schedules)})
}

func activeRuns(state ports.State) int {
	count := 0
	for _, run := range state.CodingRuns {
		if run.Status == "running" {
			count++
		}
	}
	return count
}

func decodeStrict(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid tool input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid tool input: trailing JSON")
	}
	return nil
}
