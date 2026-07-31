package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *TokenStore {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := OpenTokenStore(filepath.Join(t.TempDir(), "auth.json"), base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// authServer stands in for Google's token endpoint and records what the
// exchange actually sent, which is the only way to assert that the loopback
// redirect and the PKCE verifier travel with the code.
type authServer struct {
	form         url.Values
	refreshToken string
	scope        string
}

func (a *authServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.form, _ = url.ParseQuery(string(body))
		response := map[string]any{"access_token": "access-token", "token_type": "Bearer", "expires_in": 3600}
		if a.refreshToken != "" {
			response["refresh_token"] = a.refreshToken
		}
		if a.scope != "" {
			response["scope"] = a.scope
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	previous := tokenEndpoint
	tokenEndpoint = server.URL
	t.Cleanup(func() { tokenEndpoint = previous; server.Close() })
	return server
}

func testAuth(t *testing.T, store *TokenStore) *Auth {
	t.Helper()
	return NewAuth(Config{ClientID: "eggy.apps.googleusercontent.com", ClientSecret: "secret", Scopes: []string{"https://www.googleapis.com/auth/calendar"}}, store, http.DefaultClient, time.Now)
}

// TestLoginCompletesFromAPastedRedirect is the property the whole path exists
// for: nothing listens on the redirect, nothing is registered in the console,
// and the owner carries the code back by hand.
func TestLoginCompletesFromAPastedRedirect(t *testing.T) {
	endpoint := &authServer{refreshToken: "refresh-token"}
	endpoint.start(t)
	store := testStore(t)
	auth := testAuth(t, store)

	authorizationURL, err := auth.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("redirect_uri") != LoopbackRedirect {
		t.Fatalf("redirect_uri=%q, want the loopback address a desktop client needs no registration for", query.Get("redirect_uri"))
	}
	if query.Get("access_type") != "offline" || query.Get("prompt") != "consent" {
		t.Fatalf("a refresh token is not guaranteed by %s", authorizationURL)
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("state") == "" {
		t.Fatalf("authorization URL=%s", authorizationURL)
	}

	if err := auth.CompleteLogin(context.Background(), "4/code", query.Get("state")); err != nil {
		t.Fatal(err)
	}
	if endpoint.form.Get("code_verifier") == "" || endpoint.form.Get("redirect_uri") != LoopbackRedirect {
		t.Fatalf("exchange sent %v", endpoint.form)
	}
	record, err := store.Load()
	if err != nil || !record.Authorized() || record.RefreshToken != "refresh-token" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	if record.State != "" || record.CodeVerifier != "" {
		t.Fatalf("pending login survived the exchange: %#v", record)
	}
}

// A bare code carries no state. The pending window is what bounds it, and
// nothing unauthenticated can reach this entry point at all.
func TestLoginAcceptsABareCodeAndRejectsAStaleOne(t *testing.T) {
	endpoint := &authServer{refreshToken: "refresh-token"}
	endpoint.start(t)
	store := testStore(t)
	auth := testAuth(t, store)
	if _, err := auth.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := auth.CompleteLogin(context.Background(), "4/code", ""); err != nil {
		t.Fatalf("bare code rejected: %v", err)
	}
	if err := auth.CompleteLogin(context.Background(), "4/code", ""); err == nil {
		t.Fatal("a spent pending login accepted a second code")
	}

	if _, err := auth.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(record *TokenRecord) error {
		record.StateExpires = time.Now().Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := auth.CompleteLogin(context.Background(), "4/code", ""); err == nil {
		t.Fatal("expired pending login accepted")
	}
}

// Without a refresh token the grant dies at the first expiry and every product
// fails at once, so the login refuses rather than reporting success.
func TestLoginRequiresARefreshToken(t *testing.T) {
	endpoint := &authServer{}
	endpoint.start(t)
	auth := testAuth(t, testStore(t))
	if _, err := auth.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := auth.CompleteLogin(context.Background(), "4/code", "")
	if err == nil || !strings.Contains(err.Error(), "refresh token") {
		t.Fatalf("error=%v", err)
	}
}

// The consent screen can narrow what was asked for. Storing the request rather
// than the grant produces a 403 later that reads like a broken API.
func TestGrantedScopesAreStored(t *testing.T) {
	endpoint := &authServer{refreshToken: "refresh-token", scope: "https://www.googleapis.com/auth/calendar.readonly"}
	endpoint.start(t)
	store := testStore(t)
	auth := testAuth(t, store)
	if _, err := auth.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := auth.CompleteLogin(context.Background(), "4/code", ""); err != nil {
		t.Fatal(err)
	}
	record, _ := store.Load()
	if len(record.Scopes) != 1 || record.Scopes[0] != "https://www.googleapis.com/auth/calendar.readonly" {
		t.Fatalf("scopes=%v, want what the grant carries", record.Scopes)
	}
}

func TestUnauthorizedBeforeLogin(t *testing.T) {
	workspace := NewWorkspace(testAuth(t, testStore(t)), Config{})
	if _, err := workspace.CalendarList(context.Background(), "", "", "", time.Now()); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("error=%v", err)
	}
}

// authorizedWorkspace wires a workspace to a local API server with a grant
// already in place, so product tests exercise request shape rather than OAuth.
func authorizedWorkspace(t *testing.T, handler http.Handler) *Workspace {
	t.Helper()
	api := httptest.NewServer(handler)
	t.Cleanup(api.Close)
	store := testStore(t)
	if err := store.Save(TokenRecord{Version: 1, AccessToken: "access-token", TokenType: "Bearer", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	workspace := NewWorkspace(testAuth(t, store), Config{})
	workspace.endpoints = Endpoints{Gmail: api.URL, Calendar: api.URL, Drive: api.URL, Docs: api.URL, Sheets: api.URL, People: api.URL}
	return workspace
}

func TestCalendarListDefaultsToTheNextSevenDays(t *testing.T) {
	var seen url.Values
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me/calendarList") {
			_, _ = w.Write([]byte(`{"items":[{"id":"primary@example.com","summary":"Personal","primary":true}]}`))
			return
		}
		seen = r.URL.Query()
		_, _ = w.Write([]byte(`{"items":[{"id":"e1","summary":"Standup","start":{"dateTime":"2026-08-01T10:00:00Z"},"end":{"dateTime":"2026-08-01T10:30:00Z"}},{"id":"e2","summary":"Holiday","start":{"date":"2026-08-02"},"end":{"date":"2026-08-03"}}]}`))
	}))
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	events, err := workspace.CalendarList(context.Background(), "", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if seen.Get("timeMin") != now.Format(time.RFC3339) || seen.Get("timeMax") != now.Add(7*24*time.Hour).Format(time.RFC3339) {
		t.Fatalf("query=%v", seen)
	}
	if len(events) != 2 || events[0].Summary != "Standup" {
		t.Fatalf("events=%#v", events)
	}
	// An all-day event carries date, not dateTime; reporting an empty start
	// would make a whole class of entries look broken.
	if events[1].Start != "2026-08-02" {
		t.Fatalf("all-day event=%#v", events[1])
	}
}

// A bare datetime is read as UTC by Google and lands hours away, so it is
// refused before it can create anything.
func TestCalendarRefusesAnAmbiguousTime(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was sent for an ambiguous time")
	}))
	_, err := workspace.CalendarCreate(context.Background(), NewEvent{Summary: "Lunch", Start: "2026-08-01T12:00:00", End: "2026-08-01T13:00:00"})
	if err == nil || !strings.Contains(err.Error(), "timezone offset") {
		t.Fatalf("error=%v", err)
	}
}

func TestGmailSearchHydratesHeaders(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1","threadId":"t1"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"m1","threadId":"t1","snippet":"hi","labelIds":["UNREAD"],"payload":{"headers":[{"name":"From","value":"a@b.c"},{"name":"Subject","value":"Hello"}]}}`))
	}))
	messages, err := workspace.GmailSearch(context.Background(), "is:unread", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Subject != "Hello" || messages[0].From != "a@b.c" {
		t.Fatalf("messages=%#v", messages)
	}
}

// Gmail nests text/plain arbitrarily deep once a mail has attachments, so the
// body has to be found rather than read off the top level.
func TestGmailGetPrefersPlainTextAtAnyDepth(t *testing.T) {
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("the body"))
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"m1","payload":{"mimeType":"multipart/mixed","parts":[{"mimeType":"multipart/alternative","parts":[{"mimeType":"text/html","body":{"data":"PGI+bm88L2I+"}},{"mimeType":"text/plain","body":{"data":"` + encoded + `"}}]}]}}`))
	}))
	message, err := workspace.GmailGet(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if message.Body != "the body" {
		t.Fatalf("body=%q", message.Body)
	}
}

// The custom From header is what lets one mailbox carry several agents'
// names, and it must reach the wire as an RFC 5322 header.
func TestGmailSendBuildsAMimeMessage(t *testing.T) {
	var raw string
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Raw string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		decoded, _ := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(request.Raw)
		raw = string(decoded)
		_, _ = w.Write([]byte(`{"id":"sent","threadId":"t9"}`))
	}))
	sent, err := workspace.GmailSend(context.Background(), Outgoing{To: "a@b.c", Subject: "Hi", Body: "text", From: `"Eggy" <owner@example.com>`})
	if err != nil {
		t.Fatal(err)
	}
	if sent.ID != "sent" {
		t.Fatalf("sent=%#v", sent)
	}
	for _, want := range []string{"To: a@b.c", `From: "Eggy" <owner@example.com>`, "Subject: Hi", "\r\n\r\ntext"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("message missing %q:\n%s", want, raw)
		}
	}
}

// A reply that omits In-Reply-To and References opens a new conversation in
// every client the recipient might use.
func TestGmailReplyThreads(t *testing.T) {
	var raw, threadID string
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"m1","threadId":"t1","payload":{"headers":[{"name":"From","value":"a@b.c"},{"name":"Subject","value":"Question"},{"name":"Message-ID","value":"<abc@mail>"}]}}`))
			return
		}
		var request struct{ Raw, ThreadID string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		decoded, _ := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(request.Raw)
		raw, threadID = string(decoded), request.ThreadID
		_, _ = w.Write([]byte(`{"id":"sent","threadId":"t1"}`))
	}))
	if _, err := workspace.GmailReply(context.Background(), "m1", "answer", ""); err != nil {
		t.Fatal(err)
	}
	if threadID != "t1" {
		t.Fatalf("threadId=%q", threadID)
	}
	for _, want := range []string{"In-Reply-To: <abc@mail>", "References: <abc@mail>", "Subject: Re: Question", "To: a@b.c"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("reply missing %q:\n%s", want, raw)
		}
	}
}

// Google's message is the difference between a scope the consent screen
// dropped and an API never enabled in the project.
func TestAPIErrorsCarryGooglesMessage(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Google Calendar API has not been used in project 1 before or it is disabled"}}`))
	}))
	_, err := workspace.CalendarList(context.Background(), "", "", "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "has not been used in project") {
		t.Fatalf("error=%v", err)
	}
}

func TestUnauthorizedResponseIsRecognized(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"Invalid Credentials"}}`))
	}))
	_, err := workspace.ContactsList(context.Background(), 5)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("error=%v", err)
	}
}

func TestSheetsRoundTrip(t *testing.T) {
	var method, path string
	var body map[string]any
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"values":[["Name","Score"],["Alice",95]]}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"updatedCells":2,"updates":{"updatedCells":2}}`))
	}))
	values, err := workspace.SheetsGet(context.Background(), "sheet1", "Sheet1!A1:B2")
	if err != nil || len(values) != 2 {
		t.Fatalf("values=%v err=%v", values, err)
	}
	if cells, err := workspace.SheetsAppend(context.Background(), "sheet1", "Sheet1!A:B", [][]any{{"Bob", 91}}); err != nil || cells != 2 {
		t.Fatalf("cells=%d err=%v", cells, err)
	}
	if method != http.MethodPost || !strings.HasSuffix(path, ":append") {
		t.Fatalf("method=%s path=%s", method, path)
	}
}

// A product left out of config costs no schema at all -- the rule every
// configurable capability in the core follows.
func TestToolsCoverOnlyConfiguredProducts(t *testing.T) {
	workspace := NewWorkspace(testAuth(t, testStore(t)), Config{})
	tools := Tools(workspace, []string{"calendar", "gmail"}, time.Now)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Definition().Name)
	}
	if len(names) != 2 || !strings.Contains(strings.Join(names, ","), "google_calendar") || !strings.Contains(strings.Join(names, ","), "google_gmail") {
		t.Fatalf("names=%v", names)
	}
	if len(Tools(workspace, nil, time.Now)) != 0 {
		t.Fatal("an unconfigured Google still built tools")
	}
}

// Every tool must announce the one repair when the grant is missing, since a
// model reading "unauthorized" cannot otherwise tell the owner what to run.
func TestToolsPointAtTheLoginCommand(t *testing.T) {
	workspace := NewWorkspace(testAuth(t, testStore(t)), Config{})
	calls := map[string]string{
		"google_gmail":    `{"action":"search","query":"is:unread"}`,
		"google_calendar": `{"action":"list"}`,
		"google_drive":    `{"query":"notes"}`,
		"google_docs":     `{"document_id":"d1"}`,
		"google_sheets":   `{"action":"get","spreadsheet_id":"s1","range":"A1:B2"}`,
		"google_contacts": `{}`,
	}
	tools := Tools(workspace, KnownProducts(), time.Now)
	if len(tools) != len(calls) {
		t.Fatalf("built %d tools for %d products", len(tools), len(calls))
	}
	for _, tool := range tools {
		name := tool.Definition().Name
		_, err := tool.Execute(context.Background(), json.RawMessage(calls[name]))
		if err == nil || !strings.Contains(err.Error(), "/google login") {
			t.Fatalf("%s error=%v", name, err)
		}
	}
}

func TestTokenRecordIsSealedAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := OpenTokenStore(path, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(TokenRecord{Version: 1, RefreshToken: "a-very-secret-refresh-token"}); err != nil {
		t.Fatal(err)
	}
	raw, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "a-very-secret-refresh-token") {
		t.Fatal("the refresh token was written in the clear")
	}
	record, err := store.Load()
	if err != nil || record.RefreshToken != "a-very-secret-refresh-token" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func readFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	return string(body), err
}

// Reading only the primary calendar answers "nothing today" while a work or
// shared calendar is full. That is worse than an error: it is confidently
// wrong, and the answer gives the owner no way to notice.
func TestCalendarListReadsEveryCalendar(t *testing.T) {
	var read []string
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me/calendarList") {
			_, _ = w.Write([]byte(`{"items":[
{"id":"primary@example.com","summary":"Personal","primary":true,"accessRole":"owner"},
{"id":"work@example.com","summary":"Work","accessRole":"writer"},
{"id":"hidden@example.com","summary":"Old","accessRole":"reader","hidden":true}]}`))
			return
		}
		calendar := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendars/"), "/events")
		read = append(read, calendar)
		if strings.Contains(calendar, "work") {
			_, _ = w.Write([]byte(`{"items":[{"id":"w1","summary":"Standup","start":{"dateTime":"2026-08-01T09:00:00Z"},"end":{"dateTime":"2026-08-01T09:30:00Z"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"p1","summary":"Dentist","start":{"dateTime":"2026-08-01T14:00:00Z"},"end":{"dateTime":"2026-08-01T15:00:00Z"}}]}`))
	}))

	events, err := workspace.CalendarList(context.Background(), "", "", "", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("read %v, want both visible calendars and not the hidden one", read)
	}
	// Merged results arrive per calendar, each ordered on its own. A model
	// reading them out of order reports the day out of order.
	if len(events) != 2 || events[0].Summary != "Standup" || events[1].Summary != "Dentist" {
		t.Fatalf("events=%#v", events)
	}
	// Without the calendar name the model cannot say which one an event is on.
	if events[0].Calendar != "Work" || events[1].Calendar != "Personal" {
		t.Fatalf("events not labelled: %#v", events)
	}
}

// One calendar losing its grant -- a shared feed revoked, a subscription
// removed -- must not take the whole day's answer down with it.
func TestCalendarListSurvivesOneUnreadableCalendar(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me/calendarList") {
			_, _ = w.Write([]byte(`{"items":[{"id":"a@example.com","summary":"A"},{"id":"b@example.com","summary":"B"}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "b@example.com") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"insufficient permission"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"a1","summary":"Lunch","start":{"dateTime":"2026-08-01T12:00:00Z"},"end":{"dateTime":"2026-08-01T13:00:00Z"}}]}`))
	}))
	events, err := workspace.CalendarList(context.Background(), "", "", "", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 || events[0].Summary != "Lunch" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

// A named calendar is read directly: asking for one must not fan out.
func TestCalendarListHonoursANamedCalendar(t *testing.T) {
	var paths []string
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	if _, err := workspace.CalendarList(context.Background(), "work@example.com", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "work@example.com") {
		t.Fatalf("paths=%v", paths)
	}
}

func TestCalendarsListsIdsTheModelCanName(t *testing.T) {
	workspace := authorizedWorkspace(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"primary@example.com","summary":"Personal","primary":true,"accessRole":"owner"},{"id":"hidden@example.com","summary":"Old","hidden":true}]}`))
	}))
	calendars, err := workspace.Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calendars) != 1 || calendars[0].ID != "primary@example.com" || !calendars[0].Primary {
		t.Fatalf("calendars=%#v", calendars)
	}
}
