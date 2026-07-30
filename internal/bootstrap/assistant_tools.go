package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	schedulerlocal "github.com/nigelteosw/eggy/plugins/scheduler/local"
)

type bootstrapTool struct {
	definition ports.ToolDefinition
	execute    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (t bootstrapTool) Definition() ports.ToolDefinition { return t.definition }
func (t bootstrapTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return t.execute(ctx, raw)
}

func currentTimeTool(now func() time.Time, location *time.Location, timezone string) ports.Tool {
	tool := bootstrapTool{definition: toolDefinition("current_time", "Return the trusted current time and owner timezone; use this instead of model knowledge for relative dates", `{"type":"object","additionalProperties":false}`)}
	tool.execute = func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if err := strictToolDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"current_time": now().In(location).Format(time.RFC3339), "timezone": timezone})
	}
	return tool
}

// scheduleKindSchema is shared by schedule_exact and schedule_recurring: an
// optional "kind" lets the agent distinguish a plain reminder or watchdog
// notification (delivered verbatim with no later model call) from work that
// needs a self-contained agent turn to run. It defaults to "agent" so
// omitting it keeps today's behavior.
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

func scheduleTools(scheduler *schedulerlocal.Scheduler, now func() time.Time) []ports.Tool {
	exact := bootstrapTool{definition: toolDefinition("schedule_exact", "Schedule a one-time instruction at an exact RFC3339 time", `{"type":"object","properties":{"at":{"type":"string"},"instruction":{"type":"string"},`+scheduleKindSchema+`},"required":["at","instruction"],"additionalProperties":false}`)}
	exact.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			At          string `json:"at"`
			Instruction string `json:"instruction"`
			Kind        string `json:"kind"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
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
		schedule := ports.Schedule{ID: newRunID(), Kind: ports.ScheduleExact, Execution: execution, Instruction: input.Instruction, NextRun: at, Enabled: true}
		if err := scheduler.Add(ctx, schedule); err != nil {
			return nil, err
		}
		return json.Marshal(schedule)
	}
	recurring := bootstrapTool{definition: toolDefinition("schedule_recurring", "Schedule a recurring instruction with a five-field cron expression", `{"type":"object","properties":{"cron":{"type":"string"},"instruction":{"type":"string"},`+scheduleKindSchema+`},"required":["cron","instruction"],"additionalProperties":false}`)}
	recurring.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Cron        string `json:"cron"`
			Instruction string `json:"instruction"`
			Kind        string `json:"kind"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		next, err := scheduler.Next(input.Cron, now())
		if err != nil {
			return nil, err
		}
		execution, err := scheduleExecution(input.Kind)
		if err != nil {
			return nil, err
		}
		schedule := ports.Schedule{ID: newRunID(), Kind: ports.ScheduleRecurring, Execution: execution, Instruction: input.Instruction, Expression: input.Cron, NextRun: next, Enabled: true}
		if err := scheduler.Add(ctx, schedule); err != nil {
			return nil, err
		}
		return json.Marshal(schedule)
	}
	return []ports.Tool{exact, recurring}
}

func toolDefinition(name, description, schema string) ports.ToolDefinition {
	return ports.ToolDefinition{Name: name, Description: description, Schema: json.RawMessage(schema)}
}

func strictToolDecode(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
