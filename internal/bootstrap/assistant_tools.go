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

func calendarTools(calendar *services.CalendarService, channel ports.Channel, owner, defaultCalendar string, now func() time.Time, location *time.Location, timezone string) []ports.Tool {
	list := bootstrapTool{definition: toolDefinition("calendar_list", "List events across all readable calendars by default; set calendar_id only to limit the read to one calendar; use range=today, tomorrow, or this_week for relative dates so Eggy resolves trusted boundaries; reads do not require approval", `{"type":"object","properties":{"calendar_id":{"type":"string"},"range":{"type":"string","enum":["today","tomorrow","this_week"]},"from":{"type":"string"},"to":{"type":"string"}},"additionalProperties":false}`)}
	list.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			CalendarID string `json:"calendar_id"`
			Range      string `json:"range"`
			From       string `json:"from"`
			To         string `json:"to"`
		}
		if err := strictToolDecode(raw, &input); err != nil {
			return nil, err
		}
		from, to, err := resolveCalendarRange(input.Range, input.From, input.To, now(), location)
		if err != nil {
			return nil, err
		}
		resultCalendarID := input.CalendarID
		var events []ports.CalendarEvent
		if input.CalendarID == "" {
			resultCalendarID = "all"
			events, err = calendar.ListAll(ctx, from, to)
		} else {
			events, err = calendar.List(ctx, input.CalendarID, from, to)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			CalendarID string                `json:"calendar_id"`
			From       string                `json:"from"`
			To         string                `json:"to"`
			Timezone   string                `json:"timezone"`
			Events     []ports.CalendarEvent `json:"events"`
		}{CalendarID: resultCalendarID, From: from.Format(time.RFC3339), To: to.Format(time.RFC3339), Timezone: timezone, Events: events})
	}
	calendars := bootstrapTool{definition: toolDefinition("calendar_calendars", "List every calendar available to the authenticated user, including IDs, names, access roles, primary status, and hidden status", `{"type":"object","additionalProperties":false}`)}
	calendars.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if err := strictToolDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		available, err := calendar.Calendars(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(available)
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
	return []ports.Tool{list, calendars, create, update, deleteTool}
}

func resolveCalendarRange(named, rawFrom, rawTo string, now time.Time, location *time.Location) (time.Time, time.Time, error) {
	if named != "" {
		if rawFrom != "" || rawTo != "" {
			return time.Time{}, time.Time{}, errors.New("calendar range cannot be combined with from or to")
		}
		local := now.In(location)
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		switch named {
		case "today":
			return start, start.AddDate(0, 0, 1), nil
		case "tomorrow":
			start = start.AddDate(0, 0, 1)
			return start, start.AddDate(0, 0, 1), nil
		case "this_week":
			daysSinceMonday := (int(start.Weekday()) + 6) % 7
			start = start.AddDate(0, 0, -daysSinceMonday)
			return start, start.AddDate(0, 0, 7), nil
		default:
			return time.Time{}, time.Time{}, fmt.Errorf("unknown calendar range %q", named)
		}
	}
	if rawFrom == "" || rawTo == "" {
		return time.Time{}, time.Time{}, errors.New("calendar list requires range or both from and to")
	}
	from, err := time.Parse(time.RFC3339, rawFrom)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be RFC3339: %w", err)
	}
	to, err := time.Parse(time.RFC3339, rawTo)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be RFC3339: %w", err)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("calendar to must be after from")
	}
	return from, to, nil
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
