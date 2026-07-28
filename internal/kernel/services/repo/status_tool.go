package repo

import (
	"context"
	"encoding/json"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"sort"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

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
	if err := services.DecodeToolInput(raw, &struct{}{}); err != nil {
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
