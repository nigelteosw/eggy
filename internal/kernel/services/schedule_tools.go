package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ScheduleWriter is the slice of the scheduler these tools need: record a
// schedule, and resolve a cron expression's next fire time. It is declared
// here rather than imported so internal/kernel stays provider-neutral, which
// is the same reason repo.ScheduleCounter exists beside it.
type ScheduleWriter interface {
	Add(ctx context.Context, schedule ports.Schedule) error
	Next(expression string, after time.Time) (time.Time, error)
	// List and Remove are what make a created schedule reviewable. Without
	// them the agent could create a job and then never answer what it
	// created -- which also meant it could never take one back, so a wrong
	// cron expression was permanent short of editing the volume by hand.
	List(ctx context.Context) ([]ports.Schedule, error)
	Remove(ctx context.Context, id string) error
}

// NewScheduleTools returns the tools that create, review, and cancel
// schedules. newID supplies the schedule's identifier: the scheme belongs to
// the caller that owns run identity, not to the kernel.
func NewScheduleTools(schedules ScheduleWriter, now func() time.Time, newID func() string) []ports.Tool {
	return []ports.Tool{
		scheduleExactTool{schedules: schedules, newID: newID},
		scheduleRecurringTool{schedules: schedules, now: now, newID: newID},
		scheduleListTool{schedules: schedules},
		scheduleCancelTool{schedules: schedules},
	}
}

// scheduleListTool answers what is scheduled. It is read-only, so unlike the
// other three it is safe for an unprompted turn: a heartbeat asked whether
// anything is wrong should be able to see the jobs it shares a clock with.
type scheduleListTool struct{ schedules ScheduleWriter }

func (t scheduleListTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "schedule_list",
		Description: "List the schedules that exist, with their cron expression, next run, and kind",
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}
}

func (t scheduleListTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := DecodeToolInput(raw, &struct{}{}); err != nil {
		return nil, err
	}
	schedules, err := t.schedules.List(ctx)
	if err != nil {
		return nil, err
	}
	// Sorted by next run so the answer reads as a timeline rather than in
	// whatever order the store happened to walk its directory.
	sort.Slice(schedules, func(i, j int) bool { return schedules[i].NextRun.Before(schedules[j].NextRun) })
	if schedules == nil {
		schedules = []ports.Schedule{}
	}
	return json.Marshal(struct {
		Schedules []ports.Schedule `json:"schedules"`
	}{Schedules: schedules})
}

// scheduleCancelTool removes a schedule. Cancelling one that is already gone
// is not an error: the owner's intent is that it not run, and it will not.
type scheduleCancelTool struct{ schedules ScheduleWriter }

func (t scheduleCancelTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "schedule_cancel",
		Description: "Cancel a schedule by its id, so it never runs again",
		Schema:      json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
	}
}

func (t scheduleCancelTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ID) == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := t.schedules.Remove(ctx, input.ID); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Cancelled string `json:"cancelled"`
	}{Cancelled: input.ID})
}

// scheduleKindSchema is shared by both tools: an optional "kind" lets the
// agent distinguish a plain reminder or watchdog notification (delivered
// verbatim with no later model call) from work that needs a self-contained
// agent turn to run. It defaults to "agent" so omitting it keeps today's
// behavior.
const scheduleKindSchema = `"kind":{"type":"string","enum":["agent","reminder"],"description":"'reminder' delivers instruction verbatim at fire time with no model call; 'agent' (default) starts a self-contained read-only agent turn"}`

func scheduleExecution(kind string) (ports.ScheduleExecution, error) {
	switch kind {
	case "", "agent":
		return ports.ScheduleExecutionAgent, nil
	case "reminder":
		return ports.ScheduleExecutionMessage, nil
	default:
		return "", fmt.Errorf("unknown schedule kind %q", kind)
	}
}

type scheduleExactTool struct {
	schedules ScheduleWriter
	newID     func() string
}

func (t scheduleExactTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "schedule_exact",
		Description: "Schedule a one-time instruction at an exact RFC3339 time",
		Schema:      json.RawMessage(`{"type":"object","properties":{"at":{"type":"string"},"instruction":{"type":"string"},` + scheduleKindSchema + `},"required":["at","instruction"],"additionalProperties":false}`),
	}
}

func (t scheduleExactTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		At          string `json:"at"`
		Instruction string `json:"instruction"`
		Kind        string `json:"kind"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339, input.At)
	if err != nil {
		return nil, err
	}
	execution, err := scheduleExecution(input.Kind)
	if err != nil {
		return nil, err
	}
	schedule := ports.Schedule{ID: t.newID(), Kind: ports.ScheduleExact, Execution: execution, Instruction: input.Instruction, NextRun: at, Enabled: true}
	if err := t.schedules.Add(ctx, schedule); err != nil {
		return nil, err
	}
	return json.Marshal(schedule)
}

type scheduleRecurringTool struct {
	schedules ScheduleWriter
	now       func() time.Time
	newID     func() string
}

func (t scheduleRecurringTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "schedule_recurring",
		Description: "Schedule a recurring instruction with a five-field cron expression",
		Schema:      json.RawMessage(`{"type":"object","properties":{"cron":{"type":"string"},"instruction":{"type":"string"},` + scheduleKindSchema + `},"required":["cron","instruction"],"additionalProperties":false}`),
	}
}

func (t scheduleRecurringTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Cron        string `json:"cron"`
		Instruction string `json:"instruction"`
		Kind        string `json:"kind"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	next, err := t.schedules.Next(input.Cron, t.now())
	if err != nil {
		return nil, err
	}
	execution, err := scheduleExecution(input.Kind)
	if err != nil {
		return nil, err
	}
	schedule := ports.Schedule{ID: t.newID(), Kind: ports.ScheduleRecurring, Execution: execution, Instruction: input.Instruction, Expression: input.Cron, NextRun: next, Enabled: true}
	if err := t.schedules.Add(ctx, schedule); err != nil {
		return nil, err
	}
	return json.Marshal(schedule)
}
