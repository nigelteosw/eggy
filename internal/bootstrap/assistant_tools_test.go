package bootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	schedulerlocal "github.com/nigelteosw/eggy/internal/adapters/scheduler/local"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// TestScheduleToolsDistinguishReminderFromAgentExecution proves the agent
// can create a deterministic, pre-rendered reminder ("kind":"reminder") as
// well as the default self-contained agent-turn schedule, and that an
// unrecognized kind is rejected rather than silently defaulting.
func TestScheduleToolsDistinguishReminderFromAgentExecution(t *testing.T) {
	store := jsonfile.Open(filepath.Join(t.TempDir(), "state.json"))
	scheduler := schedulerlocal.New(store)
	now := func() time.Time { return time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC) }
	exact := scheduleTools(scheduler, now)[0]

	result, err := exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"Take the bins out","kind":"reminder"}`))
	if err != nil {
		t.Fatal(err)
	}
	var reminder ports.Schedule
	if err := json.Unmarshal(result, &reminder); err != nil || reminder.Execution != ports.ScheduleExecutionMessage {
		t.Fatalf("reminder=%s err=%v", result, err)
	}

	result, err = exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"Check my calendar for conflicts"}`))
	if err != nil {
		t.Fatal(err)
	}
	var agentSchedule ports.Schedule
	if err := json.Unmarshal(result, &agentSchedule); err != nil || agentSchedule.Execution != ports.ScheduleExecutionAgent {
		t.Fatalf("default schedule=%s err=%v", result, err)
	}

	if _, err := exact.Execute(context.Background(), json.RawMessage(`{"at":"2026-07-19T12:00:00Z","instruction":"x","kind":"nonsense"}`)); err == nil {
		t.Fatal("expected an unknown kind to be rejected")
	}
}

func TestCurrentTimeToolReturnsTrustedZonedClock(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Singapore")
	now := func() time.Time { return time.Date(2026, 7, 19, 12, 34, 56, 0, location) }
	result, err := currentTimeTool(now, location, "Asia/Singapore").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"current_time":"2026-07-19T12:34:56+08:00"`) || !strings.Contains(string(result), `"timezone":"Asia/Singapore"`) {
		t.Fatalf("result=%s", result)
	}
}

func TestCalendarListRangesResolveInOwnerTimezone(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Singapore")
	now := func() time.Time { return time.Date(2026, 7, 19, 12, 34, 56, 0, location) }
	provider := &recordingCalendarProvider{events: []ports.CalendarEvent{{ID: "event-1", Title: "Lunch"}}}
	calendar := services.NewCalendarService(provider, nil, nil)
	tools := calendarTools(calendar, noopChannel{}, "42", "primary", now, location, "Asia/Singapore")
	list := tools[0]

	tests := []struct {
		name, input, from, to string
	}{
		{"today", `{"calendar_id":"primary","range":"today"}`, "2026-07-19T00:00:00+08:00", "2026-07-20T00:00:00+08:00"},
		{"tomorrow", `{"calendar_id":"primary","range":"tomorrow"}`, "2026-07-20T00:00:00+08:00", "2026-07-21T00:00:00+08:00"},
		{"this week", `{"calendar_id":"primary","range":"this_week"}`, "2026-07-13T00:00:00+08:00", "2026-07-20T00:00:00+08:00"},
		{"explicit", `{"calendar_id":"primary","from":"2026-08-01T09:00:00+08:00","to":"2026-08-01T17:00:00+08:00"}`, "2026-08-01T09:00:00+08:00", "2026-08-01T17:00:00+08:00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := list.Execute(context.Background(), json.RawMessage(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if provider.from.Format(time.RFC3339) != test.from || provider.to.Format(time.RFC3339) != test.to {
				t.Fatalf("provider range=%s to %s", provider.from.Format(time.RFC3339), provider.to.Format(time.RFC3339))
			}
			var envelope struct {
				CalendarID string                `json:"calendar_id"`
				From       string                `json:"from"`
				To         string                `json:"to"`
				Timezone   string                `json:"timezone"`
				Events     []ports.CalendarEvent `json:"events"`
			}
			if err := json.Unmarshal(result, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.CalendarID != "primary" || envelope.From != test.from || envelope.To != test.to || envelope.Timezone != "Asia/Singapore" || len(envelope.Events) != 1 {
				t.Fatalf("envelope=%s", result)
			}
		})
	}

	for _, input := range []string{
		`{"range":"today","from":"2026-07-19T00:00:00+08:00","to":"2026-07-20T00:00:00+08:00"}`,
		`{"from":"2026-07-19T00:00:00+08:00"}`,
		`{"range":"last_year"}`,
		`{"from":"2026-07-20T00:00:00+08:00","to":"2026-07-19T00:00:00+08:00"}`,
	} {
		if _, err := list.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("accepted invalid input %s", input)
		}
	}
}

func TestCalendarListReadsAcrossAllCalendarsWhenCalendarIDOmitted(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Singapore")
	provider := &recordingCalendarProvider{
		calendars: []ports.CalendarInfo{{ID: "primary", AccessRole: "owner"}, {ID: "team", AccessRole: "reader"}},
		eventsByCalendar: map[string][]ports.CalendarEvent{
			"primary": {{ID: "personal", Start: time.Date(2026, 7, 20, 12, 0, 0, 0, location)}},
			"team":    {{ID: "work", Start: time.Date(2026, 7, 20, 9, 0, 0, 0, location)}},
		},
	}
	calendar := services.NewCalendarService(provider, nil, nil)
	list := calendarTools(calendar, noopChannel{}, "42", "primary", func() time.Time {
		return time.Date(2026, 7, 20, 8, 0, 0, 0, location)
	}, location, "Asia/Singapore")[0]

	result, err := list.Execute(context.Background(), json.RawMessage(`{"range":"today"}`))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		CalendarID string                `json:"calendar_id"`
		Events     []ports.CalendarEvent `json:"events"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.CalendarID != "all" || len(envelope.Events) != 2 || envelope.Events[0].ID != "work" {
		t.Fatalf("envelope=%s", result)
	}
}

func TestCalendarCalendarsListsAccessibleCalendarMetadata(t *testing.T) {
	location := time.UTC
	provider := &recordingCalendarProvider{calendars: []ports.CalendarInfo{
		{ID: "primary", Name: "Personal", AccessRole: "owner", Primary: true},
		{ID: "team", Name: "Team", AccessRole: "reader"},
		{ID: "hidden", Name: "Hidden", AccessRole: "reader", Hidden: true},
	}}
	calendar := services.NewCalendarService(provider, nil, nil)
	tools := calendarTools(calendar, noopChannel{}, "42", "primary", time.Now, location, "UTC")
	var discovery ports.Tool
	for _, tool := range tools {
		if tool.Definition().Name == "calendar_calendars" {
			discovery = tool
			break
		}
	}
	if discovery == nil {
		t.Fatal("calendar_calendars tool is missing")
	}
	result, err := discovery.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var calendars []ports.CalendarInfo
	if err := json.Unmarshal(result, &calendars); err != nil {
		t.Fatal(err)
	}
	if len(calendars) != 2 || calendars[0].Name != "Personal" || calendars[1].ID != "team" || calendars[1].Hidden {
		t.Fatalf("calendars=%s", result)
	}
	if !strings.Contains(string(result), `"hidden":false`) || !strings.Contains(string(result), `"primary":false`) {
		t.Fatalf("calendar statuses must be explicit: %s", result)
	}
}

type recordingCalendarProvider struct {
	from, to         time.Time
	events           []ports.CalendarEvent
	calendars        []ports.CalendarInfo
	eventsByCalendar map[string][]ports.CalendarEvent
}

func (p *recordingCalendarProvider) AuthorizationURL(string) string { return "" }
func (p *recordingCalendarProvider) ExchangeCode(context.Context, string) (ports.CalendarAuth, error) {
	return ports.CalendarAuth{}, nil
}

func (p *recordingCalendarProvider) ListCalendars(context.Context) ([]ports.CalendarInfo, error) {
	return p.calendars, nil
}
func (p *recordingCalendarProvider) List(_ context.Context, calendarID string, from, to time.Time) ([]ports.CalendarEvent, error) {
	p.from, p.to = from, to
	if p.eventsByCalendar != nil {
		return p.eventsByCalendar[calendarID], nil
	}
	return p.events, nil
}
func (p *recordingCalendarProvider) Create(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	return event, nil
}
func (p *recordingCalendarProvider) Update(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	return event, nil
}
func (p *recordingCalendarProvider) Delete(context.Context, string, string, string) error { return nil }
