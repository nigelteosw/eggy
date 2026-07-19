package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ApprovalRequester interface {
	Request(context.Context, approvals.Action, any, string) (approvals.Approval, error)
}

type CalendarService struct {
	provider  ports.CalendarProvider
	requester ApprovalRequester
	policy    ports.ApprovalPolicy
}

func NewCalendarService(provider ports.CalendarProvider, requester ApprovalRequester, policy ports.ApprovalPolicy) *CalendarService {
	return &CalendarService{provider: provider, requester: requester, policy: policy}
}

func (s *CalendarService) List(ctx context.Context, calendar string, start, end time.Time) ([]ports.CalendarEvent, error) {
	return s.provider.List(ctx, calendar, start, end)
}

func (s *CalendarService) Calendars(ctx context.Context) ([]ports.CalendarInfo, error) {
	return s.provider.ListCalendars(ctx)
}

func (s *CalendarService) ListAll(ctx context.Context, start, end time.Time) ([]ports.CalendarEvent, error) {
	calendars, err := s.provider.ListCalendars(ctx)
	if err != nil {
		return nil, err
	}
	events := make([]ports.CalendarEvent, 0)
	for _, calendar := range calendars {
		if !calendarEventsReadable(calendar.AccessRole) {
			continue
		}
		calendarEvents, err := s.provider.List(ctx, calendar.ID, start, end)
		if err != nil {
			return nil, fmt.Errorf("list calendar %q: %w", calendar.ID, err)
		}
		for index := range calendarEvents {
			calendarEvents[index].CalendarID = calendar.ID
		}
		events = append(events, calendarEvents...)
	}
	sort.SliceStable(events, func(left, right int) bool {
		if !events[left].Start.Equal(events[right].Start) {
			return events[left].Start.Before(events[right].Start)
		}
		if events[left].CalendarID != events[right].CalendarID {
			return events[left].CalendarID < events[right].CalendarID
		}
		return events[left].ID < events[right].ID
	})
	return events, nil
}

func calendarEventsReadable(accessRole string) bool {
	switch accessRole {
	case "reader", "writer", "writerWithoutPrivateAccess", "owner":
		return true
	default:
		return false
	}
}

type calendarMutationPayload struct {
	ID             string    `json:"id,omitempty"`
	CalendarID     string    `json:"calendar_id"`
	Title          string    `json:"title,omitempty"`
	Description    string    `json:"description,omitempty"`
	Start          time.Time `json:"start,omitempty"`
	End            time.Time `json:"end,omitempty"`
	Participants   []string  `json:"participants,omitempty"`
	ETag           string    `json:"etag,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
}

func calendarPayload(event ports.CalendarEvent) calendarMutationPayload {
	return calendarMutationPayload{ID: event.ID, CalendarID: event.CalendarID, Title: event.Title, Description: event.Description, Start: event.Start, End: event.End, Participants: append([]string(nil), event.Participants...), ETag: event.ETag, IdempotencyKey: event.IdempotencyKey}
}

func (s *CalendarService) RequestCreate(ctx context.Context, event ports.CalendarEvent) (approvals.Approval, error) {
	return s.requester.Request(ctx, approvals.CalendarCreate, calendarPayload(event), fmt.Sprintf("Create %s in %s from %s to %s", event.Title, event.CalendarID, event.Start.Format(time.RFC3339), event.End.Format(time.RFC3339)))
}
func (s *CalendarService) Create(ctx context.Context, event ports.CalendarEvent, approvalID string) (ports.CalendarEvent, error) {
	payload := calendarPayload(event)
	if err := s.policy.Authorize(ctx, approvals.CalendarCreate, payload, approvalID); err != nil {
		return ports.CalendarEvent{}, err
	}
	return s.provider.Create(ctx, event)
}
func (s *CalendarService) RequestUpdate(ctx context.Context, event ports.CalendarEvent) (approvals.Approval, error) {
	return s.requester.Request(ctx, approvals.CalendarUpdate, calendarPayload(event), fmt.Sprintf("Update %s in %s", event.ID, event.CalendarID))
}
func (s *CalendarService) Update(ctx context.Context, event ports.CalendarEvent, approvalID string) (ports.CalendarEvent, error) {
	payload := calendarPayload(event)
	if err := s.policy.Authorize(ctx, approvals.CalendarUpdate, payload, approvalID); err != nil {
		return ports.CalendarEvent{}, err
	}
	return s.provider.Update(ctx, event)
}
func (s *CalendarService) RequestDelete(ctx context.Context, calendarID, eventID, etag string) (approvals.Approval, error) {
	payload := calendarMutationPayload{ID: eventID, CalendarID: calendarID, ETag: etag}
	return s.requester.Request(ctx, approvals.CalendarDelete, payload, fmt.Sprintf("Delete %s from %s", eventID, calendarID))
}
func (s *CalendarService) Delete(ctx context.Context, calendarID, eventID, etag, approvalID string) error {
	payload := calendarMutationPayload{ID: eventID, CalendarID: calendarID, ETag: etag}
	if err := s.policy.Authorize(ctx, approvals.CalendarDelete, payload, approvalID); err != nil {
		return err
	}
	return s.provider.Delete(ctx, calendarID, eventID, etag)
}

func (s *CalendarService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	var payload calendarMutationPayload
	if err := json.Unmarshal(approval.Payload, &payload); err != nil {
		return nil, err
	}
	event := ports.CalendarEvent{ID: payload.ID, CalendarID: payload.CalendarID, Title: payload.Title, Description: payload.Description, Start: payload.Start, End: payload.End, Participants: payload.Participants, ETag: payload.ETag, IdempotencyKey: payload.IdempotencyKey}
	switch approval.Action {
	case approvals.CalendarCreate:
		return s.Create(ctx, event, approval.ID)
	case approvals.CalendarUpdate:
		return s.Update(ctx, event, approval.ID)
	case approvals.CalendarDelete:
		return nil, s.Delete(ctx, payload.CalendarID, payload.ID, payload.ETag, approval.ID)
	default:
		return nil, fmt.Errorf("approval is not a Calendar action")
	}
}
