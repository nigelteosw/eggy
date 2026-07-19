package services

import (
	"context"
	"encoding/json"
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

type fakeCalendar struct {
	events                    []ports.CalendarEvent
	creates, updates, deletes int
}

func (f *fakeCalendar) AuthorizationURL(string) string { return "" }
func (f *fakeCalendar) ExchangeCode(context.Context, string) (ports.CalendarAuth, error) {
	return ports.CalendarAuth{}, nil
}
func (f *fakeCalendar) List(context.Context, string, time.Time, time.Time) ([]ports.CalendarEvent, error) {
	return f.events, nil
}
func (f *fakeCalendar) Create(_ context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	f.creates++
	event.ID = "created"
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
