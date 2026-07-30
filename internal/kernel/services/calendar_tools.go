package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ApprovalDeliverer presents an approval request to the owner. Calendar
// mutation tools need exactly this much of a channel and nothing else, so
// bootstrap injects the one callback rather than handing the service a whole
// ports.Channel.
type ApprovalDeliverer func(context.Context, approvals.Approval) error

// CalendarToolOptions is the ambient context the tools need to turn model
// input into a provider call: which calendar an omitted calendar_id means,
// and which wall clock "today" is measured against.
type CalendarToolOptions struct {
	DefaultCalendar string
	Now             func() time.Time
	Location        *time.Location
	Timezone        string
	DeliverApproval ApprovalDeliverer
}

// NewCalendarTools returns the owner-facing Calendar tools. Reads run
// directly; every mutation only ever *requests* an approval bound to that
// exact event payload, and returns awaiting_owner. Nothing here can execute a
// mutation -- that happens later, in ExecuteApproved, once the owner has
// approved the specific payload.
func NewCalendarTools(calendar *CalendarService, options CalendarToolOptions) []ports.Tool {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Location == nil {
		options.Location = time.UTC
	}
	return []ports.Tool{
		calendarListTool{calendar: calendar, options: options},
		calendarCalendarsTool{calendar: calendar},
		calendarCreateTool{calendar: calendar, options: options},
		calendarUpdateTool{calendar: calendar, options: options},
		calendarDeleteTool{calendar: calendar, options: options},
	}
}

type calendarListTool struct {
	calendar *CalendarService
	options  CalendarToolOptions
}

func (t calendarListTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "calendar_list",
		Description: "List events across all non-hidden readable calendars by default; set calendar_id only to limit the read to one calendar; use range=today, tomorrow, or this_week for relative dates so Eggy resolves trusted boundaries; reads do not require approval",
		Schema:      json.RawMessage(`{"type":"object","properties":{"calendar_id":{"type":"string"},"range":{"type":"string","enum":["today","tomorrow","this_week"]},"from":{"type":"string"},"to":{"type":"string"}},"additionalProperties":false}`),
	}
}

func (t calendarListTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		CalendarID string `json:"calendar_id"`
		Range      string `json:"range"`
		From       string `json:"from"`
		To         string `json:"to"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	from, to, err := ResolveCalendarRange(input.Range, input.From, input.To, t.options.Now(), t.options.Location)
	if err != nil {
		return nil, err
	}
	resultCalendarID := input.CalendarID
	var events []ports.CalendarEvent
	if input.CalendarID == "" {
		resultCalendarID = "all"
		events, err = t.calendar.ListAll(ctx, from, to)
	} else {
		events, err = t.calendar.List(ctx, input.CalendarID, from, to)
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
	}{CalendarID: resultCalendarID, From: from.Format(time.RFC3339), To: to.Format(time.RFC3339), Timezone: t.options.Timezone, Events: events})
}

type calendarCalendarsTool struct{ calendar *CalendarService }

func (t calendarCalendarsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "calendar_calendars",
		Description: "List every non-hidden calendar available to the authenticated user, including IDs, names, access roles, and primary status",
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}
}

func (t calendarCalendarsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := DecodeToolInput(raw, &struct{}{}); err != nil {
		return nil, err
	}
	available, err := t.calendar.Calendars(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(available)
}

type calendarCreateTool struct {
	calendar *CalendarService
	options  CalendarToolOptions
}

func (t calendarCreateTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "calendar_create",
		Description: "Request approval to create an exact Calendar event",
		Schema:      json.RawMessage(calendarMutationSchema(false)),
	}
}

func (t calendarCreateTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	event, err := decodeCalendarMutation(raw, t.options.DefaultCalendar)
	if err != nil {
		return nil, err
	}
	if event.IdempotencyKey == "" {
		event.IdempotencyKey = newIdempotencyKey()
	}
	approval, err := t.calendar.RequestCreate(ctx, event)
	if err != nil {
		return nil, err
	}
	return awaitCalendarApproval(ctx, t.options.DeliverApproval, approval)
}

type calendarUpdateTool struct {
	calendar *CalendarService
	options  CalendarToolOptions
}

func (t calendarUpdateTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "calendar_update",
		Description: "Request approval to update an exact Calendar event",
		Schema:      json.RawMessage(calendarMutationSchema(true)),
	}
}

func (t calendarUpdateTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	event, err := decodeCalendarMutation(raw, t.options.DefaultCalendar)
	if err != nil {
		return nil, err
	}
	if event.ID == "" || event.ETag == "" {
		return nil, errors.New("id and etag are required")
	}
	approval, err := t.calendar.RequestUpdate(ctx, event)
	if err != nil {
		return nil, err
	}
	return awaitCalendarApproval(ctx, t.options.DeliverApproval, approval)
}

type calendarDeleteTool struct {
	calendar *CalendarService
	options  CalendarToolOptions
}

func (t calendarDeleteTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "calendar_delete",
		Description: "Request approval to delete an exact Calendar event",
		Schema:      json.RawMessage(`{"type":"object","properties":{"calendar_id":{"type":"string"},"id":{"type":"string"},"etag":{"type":"string"}},"required":["id","etag"],"additionalProperties":false}`),
	}
}

func (t calendarDeleteTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		CalendarID string `json:"calendar_id"`
		ID         string `json:"id"`
		ETag       string `json:"etag"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if input.CalendarID == "" {
		input.CalendarID = t.options.DefaultCalendar
	}
	approval, err := t.calendar.RequestDelete(ctx, input.CalendarID, input.ID, input.ETag)
	if err != nil {
		return nil, err
	}
	return awaitCalendarApproval(ctx, t.options.DeliverApproval, approval)
}

func awaitCalendarApproval(ctx context.Context, deliver ApprovalDeliverer, approval approvals.Approval) (json.RawMessage, error) {
	if deliver == nil {
		return nil, errors.New("no approval channel is configured")
	}
	if err := deliver(ctx, approval); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"approval_id": approval.ID, "status": "awaiting_owner"})
}

// ResolveCalendarRange turns either a named relative range or an explicit
// RFC3339 pair into concrete boundaries. Named ranges are resolved here, in
// Eggy's own clock and location, so "today" cannot be shifted by the model
// supplying its own idea of the date.
func ResolveCalendarRange(named, rawFrom, rawTo string, now time.Time, location *time.Location) (time.Time, time.Time, error) {
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
	if err := DecodeToolInput(raw, &input); err != nil {
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

func newIdempotencyKey() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
