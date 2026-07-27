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

// ScheduleCounter reports how many scheduled jobs exist. Schedules live as
// files in <home>/cron rather than in state.json, so the status tool asks
// the scheduler instead of counting a map.
type ScheduleCounter interface {
	List(context.Context) ([]ports.Schedule, error)
}

type statusTool struct {
	store     ports.StateStore
	changes   *Changes
	schedules ScheduleCounter
}

func NewStatusTool(store ports.StateStore, changes *Changes, schedules ScheduleCounter) ports.Tool {
	return statusTool{store: store, changes: changes, schedules: schedules}
}
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
	repositories := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		repositories = append(repositories, name)
	}
	sort.Strings(repositories)
	active, err := activeRuns(ctx, t.changes)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Repositories     []string `json:"repositories,omitempty"`
		ActiveRuns       int      `json:"active_runs"`
		PendingApprovals int      `json:"pending_approvals"`
		Schedules        int      `json:"schedules"`
	}{Repositories: repositories, ActiveRuns: active, PendingApprovals: pending, Schedules: scheduleCount(ctx, t.schedules)})
}

// scheduleCount treats an unreadable cron directory as zero rather than
// failing the whole status read: one broken job file should not deny the
// agent every other fact about its own state.
func scheduleCount(ctx context.Context, schedules ScheduleCounter) int {
	if schedules == nil {
		return 0
	}
	all, err := schedules.List(ctx)
	if err != nil {
		return 0
	}
	return len(all)
}

func activeRuns(ctx context.Context, changes *Changes) (int, error) {
	if changes == nil {
		return 0, nil
	}
	all, err := changes.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, change := range all {
		if change.Phase == ports.PhaseRunning {
			count++
		}
	}
	return count, nil
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
