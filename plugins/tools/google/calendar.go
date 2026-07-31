package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Event struct {
	ID          string   `json:"id"`
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

// CalendarList defaults to the next seven days when the window is left open,
// which is what "what's on my calendar" almost always means and what Hermes'
// CLI defaults to.
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
	values := url.Values{"timeMin": {from}, "timeMax": {to}, "singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"50"}}
	var response struct {
		Items []calendarEvent `json:"items"`
	}
	if err := w.call(ctx, http.MethodGet, w.endpoints.Calendar+"/calendars/"+url.PathEscape(calendarOrDefault(calendarID))+"/events", values, nil, &response); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(response.Items))
	for _, item := range response.Items {
		events = append(events, item.event())
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
