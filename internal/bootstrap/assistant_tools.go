package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	schedulerlocal "github.com/nigelteosw/eggy/internal/adapters/scheduler/local"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type bootstrapTool struct {
	definition ports.ToolDefinition
	execute    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (t bootstrapTool) Definition() ports.ToolDefinition { return t.definition }
func (t bootstrapTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return t.execute(ctx, raw)
}

func calendarTools(calendar *services.CalendarService, channel ports.Channel, owner, defaultCalendar string) []ports.Tool {
	list := bootstrapTool{definition: toolDefinition("calendar_list", "List Calendar events; reads do not require approval", `{"type":"object","properties":{"calendar_id":{"type":"string"},"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"],"additionalProperties":false}`)}
	list.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			CalendarID string `json:"calendar_id"`
			From       string `json:"from"`
			To         string `json:"to"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		if input.CalendarID == "" {
			input.CalendarID = defaultCalendar
		}
		from, err := time.Parse(time.RFC3339, input.From)
		if err != nil {
			return nil, fmt.Errorf("from must be RFC3339: %w", err)
		}
		to, err := time.Parse(time.RFC3339, input.To)
		if err != nil {
			return nil, fmt.Errorf("to must be RFC3339: %w", err)
		}
		events, err := calendar.List(ctx, input.CalendarID, from, to)
		if err != nil {
			return nil, err
		}
		return json.Marshal(events)
	}
	create := bootstrapTool{definition: toolDefinition("calendar_create", "Request approval to create an exact Calendar event", calendarMutationSchema(false))}
	create.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		event, err := decodeCalendarMutation(raw, defaultCalendar)
		if err != nil {
			return nil, err
		}
		if event.IdempotencyKey == "" {
			event.IdempotencyKey = newRunID()
		}
		approval, err := calendar.RequestCreate(ctx, event)
		if err != nil {
			return nil, err
		}
		if err := channel.DeliverApproval(ctx, owner, approval); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"approval_id": approval.ID, "status": "awaiting_owner"})
	}
	update := bootstrapTool{definition: toolDefinition("calendar_update", "Request approval to update an exact Calendar event", calendarMutationSchema(true))}
	update.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		event, err := decodeCalendarMutation(raw, defaultCalendar)
		if err != nil {
			return nil, err
		}
		if event.ID == "" || event.ETag == "" {
			return nil, errors.New("id and etag are required")
		}
		approval, err := calendar.RequestUpdate(ctx, event)
		if err != nil {
			return nil, err
		}
		if err := channel.DeliverApproval(ctx, owner, approval); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"approval_id": approval.ID, "status": "awaiting_owner"})
	}
	deleteTool := bootstrapTool{definition: toolDefinition("calendar_delete", "Request approval to delete an exact Calendar event", `{"type":"object","properties":{"calendar_id":{"type":"string"},"id":{"type":"string"},"etag":{"type":"string"}},"required":["id","etag"],"additionalProperties":false}`)}
	deleteTool.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			CalendarID string `json:"calendar_id"`
			ID         string `json:"id"`
			ETag       string `json:"etag"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		if input.CalendarID == "" {
			input.CalendarID = defaultCalendar
		}
		approval, err := calendar.RequestDelete(ctx, input.CalendarID, input.ID, input.ETag)
		if err != nil {
			return nil, err
		}
		if err := channel.DeliverApproval(ctx, owner, approval); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"approval_id": approval.ID, "status": "awaiting_owner"})
	}
	return []ports.Tool{list, create, update, deleteTool}
}

func scheduleTools(scheduler *schedulerlocal.Scheduler, now func() time.Time) []ports.Tool {
	exact := bootstrapTool{definition: toolDefinition("schedule_exact", "Schedule a one-time instruction at an exact RFC3339 time", `{"type":"object","properties":{"at":{"type":"string"},"instruction":{"type":"string"}},"required":["at","instruction"],"additionalProperties":false}`)}
	exact.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			At          string `json:"at"`
			Instruction string `json:"instruction"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339, input.At)
		if err != nil {
			return nil, err
		}
		schedule := ports.Schedule{ID: newRunID(), Kind: ports.ScheduleExact, Instruction: input.Instruction, NextRun: at, Enabled: true}
		if err := scheduler.Add(ctx, schedule); err != nil {
			return nil, err
		}
		return json.Marshal(schedule)
	}
	recurring := bootstrapTool{definition: toolDefinition("schedule_recurring", "Schedule a recurring instruction with a five-field cron expression", `{"type":"object","properties":{"cron":{"type":"string"},"instruction":{"type":"string"}},"required":["cron","instruction"],"additionalProperties":false}`)}
	recurring.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Cron        string `json:"cron"`
			Instruction string `json:"instruction"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		next, err := scheduler.Next(input.Cron, now())
		if err != nil {
			return nil, err
		}
		schedule := ports.Schedule{ID: newRunID(), Kind: ports.ScheduleRecurring, Instruction: input.Instruction, Expression: input.Cron, NextRun: next, Enabled: true}
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

func calendarMutationSchema(requireID bool) string {
	required := `"title","start","end"`
	if requireID {
		required = `"id","etag",` + required
	}
	return `{"type":"object","properties":{"id":{"type":"string"},"calendar_id":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"start":{"type":"string"},"end":{"type":"string"},"participants":{"type":"array","items":{"type":"string"}},"etag":{"type":"string"},"idempotency_key":{"type":"string"}},"required":[` + required + `],"additionalProperties":false}`
}

func decodeCalendarMutation(raw json.RawMessage, defaultCalendar string) (ports.CalendarEvent, error) {
	var input struct {
		ID             string   `json:"id"`
		CalendarID     string   `json:"calendar_id"`
		Title          string   `json:"title"`
		Description    string   `json:"description"`
		Start          string   `json:"start"`
		End            string   `json:"end"`
		Participants   []string `json:"participants"`
		ETag           string   `json:"etag"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	if err := strictToolDecode(raw, &input); err != nil {
		return ports.CalendarEvent{}, err
	}
	start, err := time.Parse(time.RFC3339, input.Start)
	if err != nil {
		return ports.CalendarEvent{}, fmt.Errorf("start must be RFC3339: %w", err)
	}
	end, err := time.Parse(time.RFC3339, input.End)
	if err != nil {
		return ports.CalendarEvent{}, fmt.Errorf("end must be RFC3339: %w", err)
	}
	if !end.After(start) {
		return ports.CalendarEvent{}, errors.New("calendar end must be after start")
	}
	if input.CalendarID == "" {
		input.CalendarID = defaultCalendar
	}
	return ports.CalendarEvent{ID: input.ID, CalendarID: input.CalendarID, Title: input.Title, Description: input.Description, Start: start, End: end, Participants: input.Participants, ETag: input.ETag, IdempotencyKey: input.IdempotencyKey}, nil
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
