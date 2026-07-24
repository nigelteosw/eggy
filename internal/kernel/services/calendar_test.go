package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCalendarReadsAutomaticallyAndMutationsUseExactApprovalPayload(t *testing.T) {
	provider := &fakeCalendar{events: []ports.CalendarEvent{{ID: "existing", Title: "Existing"}}}
	gateway := &fakeCalendarApprovals{}
	service := NewCalendarService(provider, gateway, gateway)
	start := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if events, err := service.List(context.Background(), "primary", start, start.Add(24*time.Hour)); err != nil || len(events) != 1 || len(gateway.requested) != 0 {
		t.Fatalf("events=%#v err=%v approvals=%v", events, err, gateway.requested)
	}

	event := ports.CalendarEvent{CalendarID: "primary", Title: "Lunch", Start: start, End: start.Add(time.Hour), Participants: []string{"a@example.com"}, IdempotencyKey: "request-1"}
	approval, err := service.RequestCreate(context.Background(), event)
	if err != nil || approval.Action != approvals.CalendarCreate {
		t.Fatalf("approval=%#v err=%v", approval, err)
	}
	created, err := service.Create(context.Background(), event, approval.ID)
	if err != nil || created.ID != "created" || provider.creates != 1 || gateway.authorized[0] != approvals.CalendarCreate {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	payload, _ := json.Marshal(gateway.payloads[0])
	for _, exact := range []string{"primary", "Lunch", "a@example.com", "request-1"} {
		if !contains(string(payload), exact) {
			t.Fatalf("payload %s missing %q", payload, exact)
		}
	}
}

func TestCalendarListAllMergesReadableCalendarsInStableOrder(t *testing.T) {
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	provider := &fakeCalendar{
		calendars: []ports.CalendarInfo{
			{ID: "primary", AccessRole: "owner"},
			{ID: "team", AccessRole: "reader"},
			{ID: "shared", AccessRole: "writerWithoutPrivateAccess"},
			{ID: "hidden", AccessRole: "reader", Hidden: true},
			{ID: "availability", AccessRole: "freeBusyReader"},
			{ID: "revoked", AccessRole: "none"},
		},
		eventsByCalendar: map[string][]ports.CalendarEvent{
			"primary": {{ID: "later", Start: start.Add(12 * time.Hour)}},
			"team":    {{ID: "earlier", Start: start.Add(10 * time.Hour)}},
			"shared":  {{ID: "same-time", Start: start.Add(12 * time.Hour)}},
			"hidden":  {{ID: "private", Start: start.Add(8 * time.Hour)}},
		},
	}
	service := NewCalendarService(provider, nil, nil)

	events, err := service.ListAll(context.Background(), start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].ID != "earlier" || events[1].CalendarID != "primary" || events[2].CalendarID != "shared" {
		t.Fatalf("events=%#v", events)
	}
	if got := strings.Join(provider.listedCalendars, ","); got != "primary,team,shared" {
		t.Fatalf("listed calendars=%s", got)
	}
}

func TestCalendarCalendarsReturnsOnlyVisibleProviderMetadata(t *testing.T) {
	provider := &fakeCalendar{calendars: []ports.CalendarInfo{
		{ID: "primary", Name: "Personal", AccessRole: "owner", Primary: true},
		{ID: "hidden", Name: "Hidden", AccessRole: "reader", Hidden: true},
	}}
	service := NewCalendarService(provider, nil, nil)

	calendars, err := service.Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calendars) != 1 || calendars[0].Name != "Personal" || !calendars[0].Primary {
		t.Fatalf("calendars=%#v", calendars)
	}
}

func TestCalendarUpdateAndDeleteBindETag(t *testing.T) {
	provider := &fakeCalendar{}
	gateway := &fakeCalendarApprovals{}
	service := NewCalendarService(provider, gateway, gateway)
	event := ports.CalendarEvent{ID: "event-1", CalendarID: "primary", Title: "Changed", Start: time.Now(), End: time.Now().Add(time.Hour), ETag: "etag-1"}
	approval, _ := service.RequestUpdate(context.Background(), event)
	if _, err := service.Update(context.Background(), event, approval.ID); err != nil {
		t.Fatal(err)
	}
	deleteApproval, _ := service.RequestDelete(context.Background(), "primary", "event-1", "etag-2")
	if err := service.Delete(context.Background(), "primary", "event-1", "etag-2", deleteApproval.ID); err != nil {
		t.Fatal(err)
	}
	updatePayload, _ := json.Marshal(gateway.payloads[0])
	deletePayload, _ := json.Marshal(gateway.payloads[1])
	if !contains(string(updatePayload), "etag-1") || !contains(string(deletePayload), "etag-2") {
		t.Fatalf("payloads=%s %s", updatePayload, deletePayload)
	}
}

func TestCalendarResumesPersistedApprovedMutation(t *testing.T) {
	provider := &fakeCalendar{}
	gateway := &fakeCalendarApprovals{}
	service := NewCalendarService(provider, gateway, gateway)
	event := ports.CalendarEvent{CalendarID: "primary", Title: "Lunch", Start: time.Now(), End: time.Now().Add(time.Hour), IdempotencyKey: "key"}
	approval, _ := service.RequestCreate(context.Background(), event)
	approval.Payload, _ = json.Marshal(calendarPayload(event))
	if _, err := service.ExecuteApproved(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if provider.creates != 1 {
		t.Fatalf("creates=%d", provider.creates)
	}
}

// TestCalendarExecuteApprovedFormatsCreateOutcomeWithCalendarNameAndURL
// covers the approval-completion message App.handleApproval renders with a
// bare %v: before CalendarMutationOutcome existed, that printed
// ports.CalendarEvent's raw Go struct fields (e.g. a
// "...@group.calendar.google.com" address, never a name the owner
// recognizes) instead of something readable.
func TestCalendarExecuteApprovedFormatsCreateOutcomeWithCalendarNameAndURL(t *testing.T) {
	provider := &fakeCalendar{calendars: []ports.CalendarInfo{{ID: "cal-1", Name: "🤍"}}}
	gateway := &fakeCalendarApprovals{}
	service := NewCalendarService(provider, gateway, gateway)
	event := ports.CalendarEvent{CalendarID: "cal-1", Title: "[L+N] Zoo", Start: time.Now(), End: time.Now().Add(time.Hour), IdempotencyKey: "key"}
	approval, _ := service.RequestCreate(context.Background(), event)
	approval.Payload, _ = json.Marshal(calendarPayload(event))

	result, err := service.ExecuteApproved(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := result.(CalendarMutationOutcome)
	if !ok {
		t.Fatalf("result type=%T, want CalendarMutationOutcome", result)
	}
	if outcome.CalendarName != "🤍" {
		t.Fatalf("calendar name=%q, want 🤍", outcome.CalendarName)
	}
	rendered := outcome.String()
	if !strings.Contains(rendered, "Added to Calendar: 🤍") || !strings.Contains(rendered, "[L+N] Zoo") || !strings.Contains(rendered, "https://calendar.example/event/created") {
		t.Fatalf("rendered=%q", rendered)
	}
}

// TestCalendarExecuteApprovedFallsBackToRawCalendarIDWhenNameLookupFails
// ensures a Calendars() failure (e.g. a transient list error) never hides
// an already-successful create behind a hard error -- the owner still gets
// a confirmation, just with the raw ID instead of the friendly name.
func TestCalendarExecuteApprovedFallsBackToRawCalendarIDWhenNameLookupFails(t *testing.T) {
	provider := &fakeCalendar{listCalendarsErr: errors.New("calendar list unavailable")}
	gateway := &fakeCalendarApprovals{}
	service := NewCalendarService(provider, gateway, gateway)
	event := ports.CalendarEvent{CalendarID: "cal-1", Title: "Lunch", Start: time.Now(), End: time.Now().Add(time.Hour), IdempotencyKey: "key"}
	approval, _ := service.RequestCreate(context.Background(), event)
	approval.Payload, _ = json.Marshal(calendarPayload(event))

	result, err := service.ExecuteApproved(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := result.(CalendarMutationOutcome)
	if !ok {
		t.Fatalf("result type=%T, want CalendarMutationOutcome", result)
	}
	if outcome.CalendarName != "cal-1" {
		t.Fatalf("calendar name=%q, want fallback to raw id cal-1", outcome.CalendarName)
	}
}

type fakeCalendar struct {
	events                    []ports.CalendarEvent
	calendars                 []ports.CalendarInfo
	eventsByCalendar          map[string][]ports.CalendarEvent
	listedCalendars           []string
	listCalendarsErr          error
	creates, updates, deletes int
}

func (f *fakeCalendar) AuthorizationURL(string) string { return "" }
func (f *fakeCalendar) ExchangeCode(context.Context, string) (ports.CalendarAuth, error) {
	return ports.CalendarAuth{}, nil
}

func (f *fakeCalendar) ListCalendars(context.Context) ([]ports.CalendarInfo, error) {
	if f.listCalendarsErr != nil {
		return nil, f.listCalendarsErr
	}
	return f.calendars, nil
}
func (f *fakeCalendar) List(_ context.Context, calendarID string, _ time.Time, _ time.Time) ([]ports.CalendarEvent, error) {
	f.listedCalendars = append(f.listedCalendars, calendarID)
	if f.eventsByCalendar != nil {
		return f.eventsByCalendar[calendarID], nil
	}
	return f.events, nil
}
func (f *fakeCalendar) Create(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	f.creates++
	event.ID = "created"
	event.URL = "https://calendar.example/event/created"
	return event, nil
}
func (f *fakeCalendar) Update(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	f.updates++
	return event, nil
}
func (f *fakeCalendar) Delete(context.Context, string, string, string) error { f.deletes++; return nil }

type fakeCalendarApprovals struct {
	requested, authorized []approvals.Action
	payloads              []any
	counter               int
}

func (f *fakeCalendarApprovals) Request(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	f.counter++
	f.requested = append(f.requested, action)
	return approvals.Approval{ID: "approval-" + summary, Action: action}, nil
}
func (f *fakeCalendarApprovals) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	f.authorized = append(f.authorized, action)
	f.payloads = append(f.payloads, payload)
	return nil
}
