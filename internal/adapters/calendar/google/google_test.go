package google

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestAdapterOAuthExchangeRefreshAndCalendarOperations(t *testing.T) {
	cipher, _ := NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	encrypted, _ := cipher.EncryptToken("stored-refresh")
	store := &calendarStateStore{state: ports.State{SchemaVersion: 1, Calendar: ports.CalendarAuth{EncryptedRefreshToken: encrypted}}}
	var methods, paths, auth []string
	var createIDs []string
	listAttempts := 0
	client := &http.Client{Transport: calendarRoundTrip(func(request *http.Request) (*http.Response, error) {
		methods, paths, auth = append(methods, request.Method), append(paths, request.URL.Path), append(auth, request.Header.Get("Authorization"))
		switch {
		case request.URL.Path == "/token":
			_ = request.ParseForm()
			if request.Form.Get("grant_type") == "authorization_code" {
				return calendarJSON(http.StatusOK, `{"refresh_token":"new-refresh","access_token":"access","expires_in":3600}`), nil
			}
			if request.Form.Get("refresh_token") != "stored-refresh" {
				t.Fatalf("refresh form=%v", request.Form)
			}
			return calendarJSON(http.StatusOK, `{"access_token":"access","expires_in":3600}`), nil
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/events/eggy"):
			return calendarJSON(http.StatusOK, `{"id":"event-1","etag":"tag-2","summary":"Lunch","start":{"dateTime":"2026-07-20T12:00:00Z"},"end":{"dateTime":"2026-07-20T13:00:00Z"}}`), nil
		case request.Method == http.MethodGet:
			listAttempts++
			if listAttempts == 1 {
				return calendarJSON(http.StatusServiceUnavailable, `{}`), nil
			}
			return calendarJSON(http.StatusOK, `{"items":[{"id":"event-1","etag":"tag-1","summary":"Lunch","start":{"dateTime":"2026-07-20T12:00:00Z"},"end":{"dateTime":"2026-07-20T13:00:00Z"},"attendees":[{"email":"a@example.com"}]},{"id":"event-2","summary":"Holiday","start":{"date":"2026-07-21"},"end":{"date":"2026-07-22"}}]}`), nil
		case request.Method == http.MethodPost:
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			createIDs = append(createIDs, body.ID)
			if len(createIDs) == 2 {
				return calendarJSON(http.StatusConflict, `{}`), nil
			}
			return calendarJSON(http.StatusOK, `{"id":"event-1","etag":"tag-2","summary":"Lunch","start":{"dateTime":"2026-07-20T12:00:00Z"},"end":{"dateTime":"2026-07-20T13:00:00Z"}}`), nil
		case request.Method == http.MethodPatch:
			return calendarJSON(http.StatusOK, `{"id":"event-1","etag":"tag-2","summary":"Lunch","start":{"dateTime":"2026-07-20T12:00:00Z"},"end":{"dateTime":"2026-07-20T13:00:00Z"}}`), nil
		case request.Method == http.MethodDelete:
			return calendarJSON(http.StatusNoContent, ``), nil
		default:
			return calendarJSON(http.StatusBadRequest, `{}`), nil
		}
	})}
	adapter := NewAdapter(AdapterConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://eggy.test/auth/google/callback", AuthURL: "https://accounts.test/auth", TokenURL: "https://oauth.test/token", APIBase: "https://calendar.test/calendar/v3", Cipher: cipher, Store: store, HTTPClient: client})
	authURL := adapter.AuthorizationURL("signed-state")
	parsed, _ := url.Parse(authURL)
	if parsed.Query().Get("state") != "signed-state" || parsed.Query().Get("access_type") != "offline" {
		t.Fatalf("auth URL=%s", authURL)
	}
	authState, err := adapter.ExchangeCode(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := cipher.DecryptToken(authState.EncryptedRefreshToken)
	if plain != "new-refresh" {
		t.Fatalf("refresh=%q", plain)
	}

	events, err := adapter.List(context.Background(), "primary", time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 2 || events[0].Participants[0] != "a@example.com" || events[1].Start.Format("2006-01-02") != "2026-07-21" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if listAttempts != 2 {
		t.Fatalf("transient list attempted %d times", listAttempts)
	}
	event := ports.CalendarEvent{CalendarID: "primary", Title: "Lunch", Start: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC), IdempotencyKey: "request-1", ETag: "tag-1"}
	if _, err := adapter.Create(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Create(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(createIDs) != 2 || createIDs[0] == "" || createIDs[0] != createIDs[1] || !strings.HasPrefix(createIDs[0], "eggy") {
		t.Fatalf("idempotent IDs=%v paths=%v", createIDs, paths)
	}
	event.ID = "event-1"
	if _, err := adapter.Update(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Delete(context.Background(), "primary", "event-1", "tag-1"); err != nil {
		t.Fatal(err)
	}
	for index, method := range methods {
		if paths[index] != "/token" && auth[index] != "Bearer access" {
			t.Fatalf("%s %s auth=%q", method, paths[index], auth[index])
		}
	}
}

func TestAdapterListsVisibleCalendarsAndAllEventPages(t *testing.T) {
	cipher, _ := NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	encrypted, _ := cipher.EncryptToken("stored-refresh")
	store := &calendarStateStore{state: ports.State{SchemaVersion: 1, Calendar: ports.CalendarAuth{EncryptedRefreshToken: encrypted}}}
	var calendarQueries, eventQueries []url.Values
	client := &http.Client{Transport: calendarRoundTrip(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/token":
			return calendarJSON(http.StatusOK, `{"access_token":"access","expires_in":3600}`), nil
		case "/calendar/v3/users/me/calendarList":
			calendarQueries = append(calendarQueries, request.URL.Query())
			if request.URL.Query().Get("pageToken") == "calendar-page-2" {
				return calendarJSON(http.StatusOK, `{"items":[{"id":"team","summary":"Team","accessRole":"reader"}]}`), nil
			}
			return calendarJSON(http.StatusOK, `{"nextPageToken":"calendar-page-2","items":[{"id":"primary","summary":"Personal","accessRole":"owner","primary":true}]}`), nil
		case "/calendar/v3/calendars/primary/events":
			eventQueries = append(eventQueries, request.URL.Query())
			if request.URL.Query().Get("pageToken") == "event-page-2" {
				return calendarJSON(http.StatusOK, `{"items":[{"id":"event-2","summary":"Dinner","start":{"dateTime":"2026-07-20T18:00:00Z"},"end":{"dateTime":"2026-07-20T19:00:00Z"}}]}`), nil
			}
			return calendarJSON(http.StatusOK, `{"nextPageToken":"event-page-2","items":[{"id":"event-1","summary":"Lunch","start":{"dateTime":"2026-07-20T12:00:00Z"},"end":{"dateTime":"2026-07-20T13:00:00Z"}}]}`), nil
		default:
			return calendarJSON(http.StatusNotFound, `{}`), nil
		}
	})}
	adapter := NewAdapter(AdapterConfig{TokenURL: "https://calendar.test/token", APIBase: "https://calendar.test/calendar/v3", Cipher: cipher, Store: store, HTTPClient: client})

	calendars, err := adapter.ListCalendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calendars) != 2 || calendars[0].ID != "primary" || !calendars[0].Primary || calendars[1].ID != "team" || calendars[1].Hidden {
		t.Fatalf("calendars=%#v", calendars)
	}
	if len(calendarQueries) != 2 || calendarQueries[0].Get("showHidden") != "false" || calendarQueries[1].Get("pageToken") != "calendar-page-2" {
		t.Fatalf("calendar queries=%v", calendarQueries)
	}

	events, err := adapter.List(context.Background(), "primary", time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != "event-1" || events[1].ID != "event-2" {
		t.Fatalf("events=%#v", events)
	}
	if len(eventQueries) != 2 || eventQueries[1].Get("pageToken") != "event-page-2" {
		t.Fatalf("event queries=%v", eventQueries)
	}
}

func TestOAuthHandlersSignStateAndPersistEncryptedToken(t *testing.T) {
	cipher, _ := NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	enrollmentToken := "owner-enrollment-token"
	digest := sha256.Sum256([]byte(enrollmentToken))
	store := &calendarStateStore{state: ports.State{SchemaVersion: 1, Calendar: ports.CalendarAuth{EnrollmentDigest: hex.EncodeToString(digest[:]), EnrollmentExpires: time.Date(2026, 7, 19, 12, 5, 0, 0, time.UTC)}}}
	client := &http.Client{Transport: calendarRoundTrip(func(request *http.Request) (*http.Response, error) {
		return calendarJSON(http.StatusOK, `{"refresh_token":"refresh","access_token":"access","expires_in":3600}`), nil
	})}
	adapter := NewAdapter(AdapterConfig{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://eggy.test/auth/google/callback", AuthURL: "https://accounts.test/auth", TokenURL: "https://oauth.test/token", APIBase: "https://calendar.test", Cipher: cipher, Store: store, HTTPClient: client})
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	start, callback := NewOAuthHandlers(adapter, store, []byte("state-signing-key"), func() time.Time { return now })
	response := httptest.NewRecorder()
	start.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google?enrollment="+url.QueryEscape(enrollmentToken), nil))
	if response.Code != http.StatusFound {
		t.Fatalf("start status=%d", response.Code)
	}
	location, _ := url.Parse(response.Header().Get("Location"))
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("OAuth state missing")
	}
	reused := httptest.NewRecorder()
	start.ServeHTTP(reused, httptest.NewRequest(http.MethodGet, "/auth/google?enrollment="+url.QueryEscape(enrollmentToken), nil))
	if reused.Code != http.StatusForbidden {
		t.Fatalf("reused enrollment status=%d", reused.Code)
	}

	tampered := httptest.NewRecorder()
	callback.ServeHTTP(tampered, httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=x&state="+url.QueryEscape(state+"x"), nil))
	if tampered.Code != http.StatusBadRequest {
		t.Fatalf("tampered status=%d", tampered.Code)
	}
	valid := httptest.NewRecorder()
	callback.ServeHTTP(valid, httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=x&state="+url.QueryEscape(state), nil))
	if valid.Code != http.StatusNoContent {
		t.Fatalf("callback status=%d body=%s", valid.Code, valid.Body.String())
	}
	stored, _ := store.Load(context.Background())
	plain, _ := cipher.DecryptToken(stored.Calendar.EncryptedRefreshToken)
	if plain != "refresh" {
		t.Fatalf("stored refresh=%q", plain)
	}
}

type calendarStateStore struct {
	mu    sync.Mutex
	state ports.State
}

func (s *calendarStateStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}
func (s *calendarStateStore) Update(_ context.Context, expected uint64, fn func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}

type calendarRoundTrip func(*http.Request) (*http.Response, error)

func (f calendarRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
func calendarJSON(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
