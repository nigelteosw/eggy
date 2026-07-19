package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type AdapterConfig struct {
	ClientID, ClientSecret, RedirectURL string
	AuthURL, TokenURL, APIBase          string
	Cipher                              *TokenCipher
	Store                               ports.StateStore
	HTTPClient                          *http.Client
}

type Adapter struct{ config AdapterConfig }

func NewAdapter(config AdapterConfig) *Adapter {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	config.APIBase = strings.TrimRight(config.APIBase, "/")
	return &Adapter{config: config}
}

func (a *Adapter) AuthorizationURL(state string) string {
	query := url.Values{"client_id": {a.config.ClientID}, "redirect_uri": {a.config.RedirectURL}, "response_type": {"code"}, "scope": {"https://www.googleapis.com/auth/calendar"}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}}
	return a.config.AuthURL + "?" + query.Encode()
}

func (a *Adapter) ExchangeCode(ctx context.Context, code string) (ports.CalendarAuth, error) {
	values := url.Values{"code": {code}, "client_id": {a.config.ClientID}, "client_secret": {a.config.ClientSecret}, "redirect_uri": {a.config.RedirectURL}, "grant_type": {"authorization_code"}}
	token, err := a.exchange(ctx, values)
	if err != nil {
		return ports.CalendarAuth{}, err
	}
	if token.RefreshToken == "" {
		return ports.CalendarAuth{}, errors.New("Google did not return a refresh token")
	}
	encrypted, err := a.config.Cipher.EncryptToken(token.RefreshToken)
	if err != nil {
		return ports.CalendarAuth{}, err
	}
	return ports.CalendarAuth{EncryptedRefreshToken: encrypted, TokenExpiry: time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)}, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (a *Adapter) exchange(ctx context.Context, values url.Values) (tokenResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.config.HTTPClient.Do(request)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("Google token request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("Google token endpoint returned HTTP %d", response.StatusCode)
	}
	var token tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		return tokenResponse{}, err
	}
	if token.AccessToken == "" {
		return tokenResponse{}, errors.New("Google returned no access token")
	}
	return token, nil
}

func (a *Adapter) accessToken(ctx context.Context) (string, error) {
	state, err := a.config.Store.Load(ctx)
	if err != nil {
		return "", err
	}
	if state.Calendar.EncryptedRefreshToken == "" {
		return "", errors.New("Google Calendar is not authorized")
	}
	refresh, err := a.config.Cipher.DecryptToken(state.Calendar.EncryptedRefreshToken)
	if err != nil {
		return "", err
	}
	token, err := a.exchange(ctx, url.Values{"refresh_token": {refresh}, "client_id": {a.config.ClientID}, "client_secret": {a.config.ClientSecret}, "grant_type": {"refresh_token"}})
	return token.AccessToken, err
}

type googleEvent struct {
	ID          string          `json:"id,omitempty"`
	ETag        string          `json:"etag,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Description string          `json:"description,omitempty"`
	Start       googleEventTime `json:"start"`
	End         googleEventTime `json:"end"`
	Attendees   []struct {
		Email string `json:"email"`
	} `json:"attendees,omitempty"`
	ExtendedProperties struct {
		Private map[string]string `json:"private,omitempty"`
	} `json:"extendedProperties,omitempty"`
}

type googleEventTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

func (a *Adapter) List(ctx context.Context, calendarID string, from, to time.Time) ([]ports.CalendarEvent, error) {
	query := url.Values{"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)}, "singleEvents": {"true"}, "orderBy": {"startTime"}}
	response, err := a.calendarRequest(ctx, http.MethodGet, "/calendars/"+url.PathEscape(calendarID)+"/events?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result struct {
		Items []googleEvent `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	events := make([]ports.CalendarEvent, 0, len(result.Items))
	for _, item := range result.Items {
		event, err := fromGoogleEvent(calendarID, item)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (a *Adapter) Create(ctx context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	if event.IdempotencyKey == "" {
		return ports.CalendarEvent{}, errors.New("calendar create requires idempotency key")
	}
	sum := sha256.Sum256([]byte(event.IdempotencyKey))
	eventID := "eggy" + hex.EncodeToString(sum[:20])
	googleValue := toGoogleEvent(event)
	googleValue.ID = eventID
	body, _ := json.Marshal(googleValue)
	response, err := a.rawCalendarRequest(ctx, http.MethodPost, "/calendars/"+url.PathEscape(event.CalendarID)+"/events", body, "")
	if err != nil {
		return ports.CalendarEvent{}, err
	}
	if response.StatusCode == http.StatusConflict {
		response.Body.Close()
		response, err = a.calendarRequest(ctx, http.MethodGet, "/calendars/"+url.PathEscape(event.CalendarID)+"/events/"+eventID, nil, "")
		if err != nil {
			return ports.CalendarEvent{}, err
		}
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		status := response.StatusCode
		response.Body.Close()
		return ports.CalendarEvent{}, fmt.Errorf("Google Calendar returned HTTP %d", status)
	}
	defer response.Body.Close()
	return decodeCalendarEvent(response, event.CalendarID)
}

func (a *Adapter) Update(ctx context.Context, event ports.CalendarEvent) (ports.CalendarEvent, error) {
	if event.ID == "" || event.ETag == "" {
		return ports.CalendarEvent{}, errors.New("calendar update requires id and etag")
	}
	body, _ := json.Marshal(toGoogleEvent(event))
	response, err := a.calendarRequest(ctx, http.MethodPatch, "/calendars/"+url.PathEscape(event.CalendarID)+"/events/"+url.PathEscape(event.ID), body, event.ETag)
	if err != nil {
		return ports.CalendarEvent{}, err
	}
	defer response.Body.Close()
	return decodeCalendarEvent(response, event.CalendarID)
}

func (a *Adapter) Delete(ctx context.Context, calendarID, eventID, etag string) error {
	if eventID == "" || etag == "" {
		return errors.New("calendar delete requires id and etag")
	}
	response, err := a.calendarRequest(ctx, http.MethodDelete, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID), nil, etag)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (a *Adapter) calendarRequest(ctx context.Context, method, path string, body []byte, etag string) (*http.Response, error) {
	response, err := a.rawCalendarRequest(ctx, method, path, body, etag)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		status := response.StatusCode
		response.Body.Close()
		return nil, fmt.Errorf("Google Calendar returned HTTP %d", status)
	}
	return response, nil
}

func (a *Adapter) rawCalendarRequest(ctx context.Context, method, path string, body []byte, etag string) (*http.Response, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	retrySafe := method == http.MethodGet || (method == http.MethodPost && strings.HasSuffix(path, "/events"))
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, a.config.APIBase+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if etag != "" {
			request.Header.Set("If-Match", etag)
		}
		response, err := a.config.HTTPClient.Do(request)
		transient := err != nil || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		if !retrySafe || !transient || attempt == 2 {
			if err != nil {
				return nil, fmt.Errorf("Google Calendar request: %w", err)
			}
			return response, nil
		}
		if response != nil {
			response.Body.Close()
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func toGoogleEvent(event ports.CalendarEvent) googleEvent {
	result := googleEvent{ID: event.ID, ETag: event.ETag, Summary: event.Title, Description: event.Description}
	result.Start.DateTime, result.End.DateTime = event.Start.Format(time.RFC3339), event.End.Format(time.RFC3339)
	for _, email := range event.Participants {
		result.Attendees = append(result.Attendees, struct {
			Email string `json:"email"`
		}{Email: email})
	}
	if event.IdempotencyKey != "" {
		result.ExtendedProperties.Private = map[string]string{"eggy_idempotency_key": event.IdempotencyKey}
	}
	return result
}

func fromGoogleEvent(calendarID string, event googleEvent) (ports.CalendarEvent, error) {
	start, err := parseGoogleEventTime(event.Start)
	if err != nil {
		return ports.CalendarEvent{}, err
	}
	end, err := parseGoogleEventTime(event.End)
	if err != nil {
		return ports.CalendarEvent{}, err
	}
	result := ports.CalendarEvent{ID: event.ID, CalendarID: calendarID, Title: event.Summary, Description: event.Description, Start: start, End: end, ETag: event.ETag, IdempotencyKey: event.ExtendedProperties.Private["eggy_idempotency_key"]}
	for _, attendee := range event.Attendees {
		result.Participants = append(result.Participants, attendee.Email)
	}
	return result, nil
}

func parseGoogleEventTime(value googleEventTime) (time.Time, error) {
	if value.DateTime != "" {
		return time.Parse(time.RFC3339, value.DateTime)
	}
	if value.Date != "" {
		return time.Parse("2006-01-02", value.Date)
	}
	return time.Time{}, errors.New("Google event has no date or dateTime")
}

func decodeCalendarEvent(response *http.Response, calendarID string) (ports.CalendarEvent, error) {
	var event googleEvent
	if err := json.NewDecoder(response.Body).Decode(&event); err != nil {
		return ports.CalendarEvent{}, err
	}
	return fromGoogleEvent(calendarID, event)
}
