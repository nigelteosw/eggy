package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Calendar struct {
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Access  string `json:"access,omitempty"`
}

type Event struct {
	ID          string   `json:"id"`
	Calendar    string   `json:"calendar,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Start       string   `json:"start,omitempty"`
	End         string   `json:"end,omitempty"`
	Location    string   `json:"location,omitempty"`
	Description string   `json:"description,omitempty"`
	Attendees   []string `json:"attendees,omitempty"`
	Link        string   `json:"link,omitempty"`
}

// NewEvent is a creation request. Attendees are addresses rather than a
// richer type because that is all the model can meaningfully supply, and
// anything else would be invented.
type NewEvent struct {
	CalendarID  string
	Summary     string
	Start       string
	End         string
	Location    string
	Description string
	Attendees   []string
}

const defaultCalendar = "primary"

// maxCalendars bounds a fan-out read. Each calendar costs one request, and an
// account subscribed to a dozen holiday feeds would otherwise spend a turn
// waiting on calendars nobody asked about.
const maxCalendars = 12

// Calendars lists what the account can actually read, which is the only way a
// model can name a calendar other than the primary one. Hidden entries are
// dropped: an account unsubscribed from a calendar in the Google UI does not
// consider it theirs, and listing it invites reads nobody wants.
func (w *Workspace) Calendars(ctx context.Context) ([]Calendar, error) {
	var response struct {
		Items []struct {
			ID         string `json:"id"`
			Summary    string `json:"summary"`
			Primary    bool   `json:"primary"`
			AccessRole string `json:"accessRole"`
			Hidden     bool   `json:"hidden"`
		} `json:"items"`
	}
	values := url.Values{"minAccessRole": {"reader"}, "showHidden": {"false"}}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Calendar+"/users/me/calendarList", values, nil, &response); err != nil {
		return nil, err
	}
	calendars := make([]Calendar, 0, len(response.Items))
	for _, item := range response.Items {
		if item.Hidden {
			continue
		}
		calendars = append(calendars, Calendar{ID: item.ID, Summary: item.Summary, Primary: item.Primary, Access: item.AccessRole})
	}
	return calendars, nil
}

// CalendarList defaults to the next seven days when the window is left open,
// which is what "what's on my calendar" almost always means and what Hermes'
// CLI defaults to.
//
// With no calendar named it reads every calendar the account can see, not just
// the primary one. Answering "nothing today" from the primary calendar alone,
// while a work or shared calendar is full, is worse than an error: it is
// confidently wrong, and the owner has no way to tell from the answer.
func (w *Workspace) CalendarList(ctx context.Context, calendarID, start, end string, now time.Time) ([]Event, error) {
	from, err := rfc3339(start)
	if err != nil {
		return nil, err
	}
	to, err := rfc3339(end)
	if err != nil {
		return nil, err
	}
	if from == "" {
		from = now.Format(time.RFC3339)
	}
	if to == "" {
		to = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	if strings.TrimSpace(calendarID) != "" {
		return w.eventsIn(ctx, calendarID, calendarID, from, to)
	}
	calendars, err := w.Calendars(ctx)
	if err != nil {
		// A calendar list the account cannot read is not a reason to answer
		// nothing: the primary calendar is still readable, and saying so is
		// better than a failed turn.
		return w.eventsIn(ctx, defaultCalendar, "", from, to)
	}
	if len(calendars) > maxCalendars {
		calendars = calendars[:maxCalendars]
	}
	events := make([]Event, 0, len(calendars)*8)
	for _, calendar := range calendars {
		found, err := w.eventsIn(ctx, calendar.ID, calendar.Summary, from, to)
		if err != nil {
			// One unreadable calendar must not take the whole answer down --
			// a subscribed feed can vanish or lose its grant independently.
			continue
		}
		events = append(events, found...)
	}
	// Each calendar came back ordered; merged, they are not. A model reading a
	// day's schedule out of order reports it out of order.
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start < events[j].Start })
	return events, nil
}

func (w *Workspace) eventsIn(ctx context.Context, calendarID, label, from, to string) ([]Event, error) {
	values := url.Values{"timeMin": {from}, "timeMax": {to}, "singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"50"}}
	var response struct {
		Items []calendarEvent `json:"items"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Calendar+"/calendars/"+url.PathEscape(calendarOrDefault(calendarID))+"/events", values, nil, &response); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(response.Items))
	for _, item := range response.Items {
		event := item.event()
		// Named only when several calendars can appear in one answer, so a
		// single-calendar read is not padded with a field saying what the
		// caller already asked for.
		event.Calendar = label
		events = append(events, event)
	}
	return events, nil
}

type calendarEvent struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Location    string `json:"location"`
	Description string `json:"description"`
	HTMLLink    string `json:"htmlLink"`
	Start       struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"end"`
	Attendees []struct {
		Email string `json:"email"`
	} `json:"attendees"`
}

// event flattens Google's start/end union. An all-day event carries "date"
// and no "dateTime", and reporting an empty start for those would make a whole
// class of entries look broken.
func (e calendarEvent) event() Event {
	event := Event{ID: e.ID, Summary: e.Summary, Location: e.Location, Description: e.Description, Link: e.HTMLLink}
	event.Start, event.End = e.Start.DateTime, e.End.DateTime
	if event.Start == "" {
		event.Start = e.Start.Date
	}
	if event.End == "" {
		event.End = e.End.Date
	}
	for _, attendee := range e.Attendees {
		event.Attendees = append(event.Attendees, attendee.Email)
	}
	return event
}

func (w *Workspace) CalendarCreate(ctx context.Context, event NewEvent) (Event, error) {
	if strings.TrimSpace(event.Summary) == "" {
		return Event{}, errors.New("a summary is required")
	}
	start, err := rfc3339(event.Start)
	if err != nil {
		return Event{}, err
	}
	end, err := rfc3339(event.End)
	if err != nil {
		return Event{}, err
	}
	if start == "" || end == "" {
		return Event{}, errors.New("both start and end are required, with a timezone offset or Z")
	}
	request := map[string]any{
		"summary": event.Summary,
		"start":   map[string]string{"dateTime": start},
		"end":     map[string]string{"dateTime": end},
	}
	if event.Location != "" {
		request["location"] = event.Location
	}
	if event.Description != "" {
		request["description"] = event.Description
	}
	if len(event.Attendees) > 0 {
		attendees := make([]map[string]string, 0, len(event.Attendees))
		for _, address := range event.Attendees {
			if trimmed := strings.TrimSpace(address); trimmed != "" {
				attendees = append(attendees, map[string]string{"email": trimmed})
			}
		}
		request["attendees"] = attendees
	}
	var created calendarEvent
	if err := w.call(ctx, http.MethodPost, w.endpoints.Calendar+"/calendars/"+url.PathEscape(calendarOrDefault(event.CalendarID))+"/events", nil, request, &created); err != nil {
		return Event{}, err
	}
	return created.event(), nil
}

func (w *Workspace) CalendarDelete(ctx context.Context, calendarID, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("an event id is required")
	}
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", w.endpoints.Calendar, url.PathEscape(calendarOrDefault(calendarID)), url.PathEscape(eventID))
	return w.call(ctx, http.MethodDelete, endpoint, nil, nil, nil)
}

func calendarOrDefault(calendarID string) string {
	if strings.TrimSpace(calendarID) == "" {
		return defaultCalendar
	}
	return calendarID
}

// rfc3339 is the only time format these tools accept or emit. A bare datetime
// is ambiguous and Google resolves it as UTC, which silently moves an event by
// hours; Hermes documents the same rule as a warning rather than enforcing it.
func rfc3339(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if _, err := time.Parse(time.RFC3339, trimmed); err != nil {
		return "", fmt.Errorf("%q needs a timezone offset or Z (RFC 3339), otherwise it is read as UTC and lands hours away", value)
	}
	return trimmed, nil
}
