package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

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
		{"today", `{"range":"today"}`, "2026-07-19T00:00:00+08:00", "2026-07-20T00:00:00+08:00"},
		{"tomorrow", `{"range":"tomorrow"}`, "2026-07-20T00:00:00+08:00", "2026-07-21T00:00:00+08:00"},
		{"this week", `{"range":"this_week"}`, "2026-07-13T00:00:00+08:00", "2026-07-20T00:00:00+08:00"},
		{"explicit", `{"from":"2026-08-01T09:00:00+08:00","to":"2026-08-01T17:00:00+08:00"}`, "2026-08-01T09:00:00+08:00", "2026-08-01T17:00:00+08:00"},
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

type recordingCalendarProvider struct {
	from, to time.Time
	events   []ports.CalendarEvent
}

func (p *recordingCalendarProvider) AuthorizationURL(string) string { return "" }
func (p *recordingCalendarProvider) ExchangeCode(context.Context, string) (ports.CalendarAuth, error) {
	return ports.CalendarAuth{}, nil
}
func (p *recordingCalendarProvider) List(_ context.Context, _ string, from, to time.Time) ([]ports.CalendarEvent, error) {
	p.from, p.to = from, to
	return p.events, nil
}
func (p *recordingCalendarProvider) Create(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	return event, nil
}
func (p *recordingCalendarProvider) Update(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	return event, nil
}
func (p *recordingCalendarProvider) Delete(context.Context, string, string, string) error { return nil }
