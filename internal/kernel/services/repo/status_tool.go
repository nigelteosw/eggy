package repo

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/services"

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
	schedules ScheduleCounter
}

func NewStatusTool(store ports.StateStore, schedules ScheduleCounter) ports.Tool {
	return statusTool{store: store, schedules: schedules}
}
func (t statusTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: "status", Description: "Read bounded Eggy operational status", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`), Effect: ports.ReadOnlyTool()}
}
func (t statusTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := services.DecodeToolInput(raw, &struct{}{}); err != nil {
		return nil, err
	}
	state, err := t.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	// A count alone left the agent able to say only "there is one pending
	// approval" and never what it was for, which is useless to the owner: the
	// summary is the whole point of asking. Each pending approval is reported
	// with what it would do and when it was requested.
	pending := make([]pendingApproval, 0, len(state.Approvals))
	for _, approval := range state.Approvals {
		if approval.Status != approvals.Pending {
			continue
		}
		pending = append(pending, pendingApproval{
			ID:        approval.ID,
			Action:    string(approval.Action),
			Summary:   approval.Summary,
			CreatedAt: approval.CreatedAt,
			ExpiresAt: approval.ExpiresAt,
		})
	}
	slices.SortFunc(pending, func(a, b pendingApproval) int { return a.CreatedAt.Compare(b.CreatedAt) })
	repositories := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		repositories = append(repositories, name)
	}
	slices.Sort(repositories)
	return json.Marshal(struct {
		Repositories     []string          `json:"repositories,omitempty"`
		PendingApprovals int               `json:"pending_approvals"`
		Approvals        []pendingApproval `json:"approvals,omitempty"`
		Schedules        int               `json:"schedules"`
	}{
		Repositories:     repositories,
		PendingApprovals: len(pending),
		Approvals:        pending,
		Schedules:        scheduleCount(ctx, t.schedules),
	})
}

// pendingApproval is the bounded view of an approval the status tool reports:
// enough for the agent to say what is waiting, without the payload, which can
// be arbitrarily large and is not the agent's to relay.
type pendingApproval struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
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
