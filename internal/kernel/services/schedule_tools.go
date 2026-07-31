package services

import (
	"context"
	"encoding/json"
	"fmt"
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
}

// NewScheduleTools returns the two tools that create schedules. newID supplies
// the schedule's identifier: the scheme belongs to the caller that owns run
// identity, not to the kernel.
func NewScheduleTools(schedules ScheduleWriter, now func() time.Time, newID func() string) []ports.Tool {
	return []ports.Tool{
		scheduleExactTool{schedules: schedules, newID: newID},
		scheduleRecurringTool{schedules: schedules, now: now, newID: newID},
	}
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
