package google

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
	// Response is this account's own reply, when it is a guest. Without it a
	// model cannot tell an invitation still waiting on the owner from one they
	// already accepted, and both look identical in a listing.
	Response string `json:"response,omitempty"`
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
	SendUpdates string
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
	slices.SortStableFunc(events, func(a, b Event) int { return cmp.Compare(a.Start, b.Start) })
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
		Email          string `json:"email"`
		Self           bool   `json:"self"`
		Optional       bool   `json:"optional"`
		ResponseStatus string `json:"responseStatus"`
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
		if attendee.Self {
			event.Response = attendee.ResponseStatus
		}
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
	attendees := make([]map[string]string, 0, len(event.Attendees))
	for _, address := range event.Attendees {
		if trimmed := strings.TrimSpace(address); trimmed != "" {
			attendees = append(attendees, map[string]string{"email": trimmed})
		}
	}
	if len(attendees) > 0 {
		request["attendees"] = attendees
	}
	// Counted after trimming, so a list of blank strings is the empty guest
	// list it actually is rather than a reason to ask Google to notify it.
	notify, err := sendUpdates(event.SendUpdates, len(attendees))
	if err != nil {
		return Event{}, err
	}
	var created calendarEvent
	endpoint := w.endpoints.Calendar + "/calendars/" + url.PathEscape(calendarOrDefault(event.CalendarID)) + "/events"
	if err := w.call(ctx, http.MethodPost, endpoint, withSendUpdates(notify), request, &created); err != nil {
		return Event{}, err
	}
	return created.event(), nil
}

// CalendarGet reads one event. It exists because every change needs the event
// as it is now: an id from a search days ago is not a reason to believe the
// time, and CalendarRespond depends on this to avoid discarding attendees.
func (w *Workspace) CalendarGet(ctx context.Context, calendarID, eventID string) (Event, error) {
	event, err := w.rawEvent(ctx, calendarID, eventID)
	if err != nil {
		return Event{}, err
	}
	return event.event(), nil
}

func (w *Workspace) rawEvent(ctx context.Context, calendarID, eventID string) (calendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return calendarEvent{}, errors.New("an event id is required")
	}
	var event calendarEvent
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", w.endpoints.Calendar, url.PathEscape(calendarOrDefault(calendarID)), url.PathEscape(eventID))
	if err := w.call(ctx, http.MethodGet, endpoint, nil, nil, &event); err != nil {
		return calendarEvent{}, err
	}
	return event, nil
}

// EventChange is a partial update: only the fields set are sent, and only
// those change. Attendees is a pointer because "leave the guest list alone"
// and "remove every guest" are different requests and an empty slice cannot
// mean both -- Google replaces the whole array with whatever it is sent.
type EventChange struct {
	CalendarID  string
	EventID     string
	Summary     string
	Start       string
	End         string
	Location    string
	Description string
	Attendees   *[]string
	SendUpdates string
}

// CalendarUpdate patches an event in place.
//
// This is why deleting and recreating is not an acceptable substitute for
// moving a meeting: recreating produces a new event, so every guest's reply is
// discarded, the invitation thread in their mail breaks, and anything else
// holding the old id -- a reminder, a linked doc, another calendar's copy --
// now points at something that no longer exists.
func (w *Workspace) CalendarUpdate(ctx context.Context, change EventChange) (Event, error) {
	if strings.TrimSpace(change.EventID) == "" {
		return Event{}, errors.New("an event id is required")
	}
	start, err := rfc3339(change.Start)
	if err != nil {
		return Event{}, err
	}
	end, err := rfc3339(change.End)
	if err != nil {
		return Event{}, err
	}
	request := map[string]any{}
	if change.Summary != "" {
		request["summary"] = change.Summary
	}
	if start != "" {
		request["start"] = map[string]string{"dateTime": start}
	}
	if end != "" {
		request["end"] = map[string]string{"dateTime": end}
	}
	if change.Location != "" {
		request["location"] = change.Location
	}
	if change.Description != "" {
		request["description"] = change.Description
	}
	guests := 0
	if change.Attendees != nil {
		attendees := make([]map[string]string, 0, len(*change.Attendees))
		for _, address := range *change.Attendees {
			if trimmed := strings.TrimSpace(address); trimmed != "" {
				attendees = append(attendees, map[string]string{"email": trimmed})
			}
		}
		request["attendees"] = attendees
		guests = len(attendees)
	}
	if len(request) == 0 {
		return Event{}, errors.New("nothing to change")
	}
	// A time change on an existing meeting is exactly when the guests need
	// telling, and here -- unlike create -- an unchanged guest list is still a
	// guest list, so the event itself decides rather than the request.
	if guests == 0 && change.SendUpdates == "" {
		if existing, err := w.rawEvent(ctx, change.CalendarID, change.EventID); err == nil {
			guests = len(existing.Attendees)
		}
	}
	notify, err := sendUpdates(change.SendUpdates, guests)
	if err != nil {
		return Event{}, err
	}
	var updated calendarEvent
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", w.endpoints.Calendar, url.PathEscape(calendarOrDefault(change.CalendarID)), url.PathEscape(change.EventID))
	if err := w.call(ctx, http.MethodPatch, endpoint, withSendUpdates(notify), request, &updated); err != nil {
		return Event{}, err
	}
	return updated.event(), nil
}

// CalendarRespond answers an invitation.
//
// Patching attendees replaces the whole array, so the only safe way to change
// one reply is to read the event, edit that one entry, and send them all back.
// Sending just the owner's entry would silently uninvite everyone else, which
// is the kind of mistake nobody notices until the meeting is empty.
func (w *Workspace) CalendarRespond(ctx context.Context, calendarID, eventID, response string) (Event, error) {
	status := map[string]string{
		"yes": "accepted", "accepted": "accepted",
		"no": "declined", "declined": "declined",
		"maybe": "tentative", "tentative": "tentative",
	}[strings.ToLower(strings.TrimSpace(response))]
	if status == "" {
		return Event{}, fmt.Errorf("response %q must be yes, no or maybe", response)
	}
	existing, err := w.rawEvent(ctx, calendarID, eventID)
	if err != nil {
		return Event{}, err
	}
	attendees := make([]map[string]any, 0, len(existing.Attendees))
	answered := false
	for _, attendee := range existing.Attendees {
		entry := map[string]any{"email": attendee.Email}
		// Google marks the authenticated account's own row, which is the only
		// reliable way to find it: the grant's address is not necessarily the
		// address the invitation was sent to, once aliases and groups exist.
		if attendee.Self {
			entry["responseStatus"] = status
			answered = true
		} else if attendee.ResponseStatus != "" {
			// Everyone else's reply is carried back unchanged; omitting it
			// would reset them all to "no reply yet".
			entry["responseStatus"] = attendee.ResponseStatus
		}
		if attendee.Optional {
			entry["optional"] = true
		}
		attendees = append(attendees, entry)
	}
	if !answered {
		return Event{}, errors.New("this account is not a guest on that event, so there is nothing to respond to")
	}
	var updated calendarEvent
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", w.endpoints.Calendar, url.PathEscape(calendarOrDefault(calendarID)), url.PathEscape(eventID))
	if err := w.call(ctx, http.MethodPatch, endpoint, url.Values{"sendUpdates": {"all"}}, map[string]any{"attendees": attendees}, &updated); err != nil {
		return Event{}, err
	}
	return updated.event(), nil
}

// Busy is one occupied block on one calendar.
type Busy struct {
	Calendar string `json:"calendar"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

// CalendarFreeBusy answers "when am I free" in one request across every
// calendar, where CalendarList costs one request per calendar and returns
// event details nobody asked for. It reports busy blocks rather than free
// ones: the gaps depend on working hours Google does not know.
func (w *Workspace) CalendarFreeBusy(ctx context.Context, calendarIDs []string, start, end string, now time.Time) ([]Busy, error) {
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
	items := make([]map[string]string, 0, len(calendarIDs))
	for _, id := range calendarIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			items = append(items, map[string]string{"id": trimmed})
		}
	}
	if len(items) == 0 {
		// Every calendar the account can see, for the same reason CalendarList
		// reads them all: "free" that ignores the work calendar is wrong in the
		// direction that gets a meeting booked over something.
		calendars, err := w.Calendars(ctx)
		if err != nil {
			items = append(items, map[string]string{"id": defaultCalendar})
		}
		if len(calendars) > maxCalendars {
			calendars = calendars[:maxCalendars]
		}
		for _, calendar := range calendars {
			items = append(items, map[string]string{"id": calendar.ID})
		}
	}
	var response struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"calendars"`
	}
	request := map[string]any{"timeMin": from, "timeMax": to, "items": items}
	if err := w.call(ctx, http.MethodPost, w.endpoints.Calendar+"/freeBusy", nil, request, &response); err != nil {
		return nil, err
	}
	busy := make([]Busy, 0, len(response.Calendars))
	for calendar, blocks := range response.Calendars {
		// A calendar that reported an error contributes no blocks, and treating
		// that silence as free time is how something gets booked over it.
		for _, block := range blocks.Busy {
			busy = append(busy, Busy{Calendar: calendar, Start: block.Start, End: block.End})
		}
	}
	slices.SortFunc(busy, func(a, b Busy) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.Calendar, b.Calendar))
	})
	return busy, nil
}

// CalendarDelete notifies by default, because the guests of a meeting that no
// longer exists are exactly the people who need to know. Deleting an event with
// no attendees sends nothing regardless, so this costs the solo case nothing.
func (w *Workspace) CalendarDelete(ctx context.Context, calendarID, eventID, notify string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("an event id is required")
	}
	if strings.TrimSpace(notify) == "" {
		notify = "all"
	}
	notify, err := sendUpdates(notify, 0)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/calendars/%s/events/%s", w.endpoints.Calendar, url.PathEscape(calendarOrDefault(calendarID)), url.PathEscape(eventID))
	return w.call(ctx, http.MethodDelete, endpoint, withSendUpdates(notify), nil, nil)
}

// sendUpdates decides who Google tells about a change.
//
// The parameter defaults to sending nothing, which for an event with attendees
// means Eggy books a meeting and invites no one -- the guests never hear about
// it and it reaches no external calendar. Google's own reference warns that
// "none" can lose events entirely for some users. So an event with attendees
// notifies them unless the caller says otherwise, and an event with none omits
// the parameter rather than asking Google to notify an empty guest list.
func sendUpdates(explicit string, attendees int) (string, error) {
	switch trimmed := strings.TrimSpace(explicit); trimmed {
	case "all", "externalOnly", "none":
		return trimmed, nil
	case "":
		if attendees > 0 {
			return "all", nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("send_updates %q must be all, externalOnly or none", explicit)
	}
}

// withSendUpdates is the query form of the above. An empty value means the
// parameter is left off entirely, which is not the same as sending "none".
func withSendUpdates(value string) url.Values {
	if value == "" {
		return nil
	}
	return url.Values{"sendUpdates": {value}}
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
