package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/session"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// fakeAccounts is a mutable account list, so a test can remove someone
// between requests.
type fakeAccounts struct{ list map[string]AccountRecord }

func (f *fakeAccounts) Account(id string) (AccountRecord, bool) {
	record, ok := f.list[id]
	return record, ok
}

func (f *fakeAccounts) Accounts() []AccountRecord {
	records := make([]AccountRecord, 0, len(f.list))
	for _, record := range f.list {
		records = append(records, record)
	}
	return records
}

func accountWebConfig(t *testing.T, now time.Time) (WebUIConfig, *sqlitestore.Store, *fakeAccounts) {
	t.Helper()
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	accounts := &fakeAccounts{list: map[string]AccountRecord{
		"nigel":   {ID: "nigel", Email: "nigel@example.com"},
		"partner": {ID: "partner", Email: "partner@example.com"},
	}}
	cfg := WebUIConfig{
		AccountMode: true, Sessions: database, Accounts: accounts,
		PublicBaseURL: "https://eggy.example",
		Now:           func() time.Time { return now },
		Threads:       newTestMemoryStore(t), Memory: newTestMemoryStore(t),
	}
	return cfg, database, accounts
}

// signIn mints a session the way the Google callback will, and returns the
// cookie plus the CSRF token GET /api/session hands out for it.
func signIn(t *testing.T, handler http.Handler, database *sqlitestore.Store, accountID string, now time.Time) (*http.Cookie, string) {
	t.Helper()
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(context.Background(), hash, accountID, now.Add(webSessionTTL)); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: webSessionCookie, Value: raw}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Account struct{ ID, Email string }
		CSRF    string `json:"csrf"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Account.ID != accountID {
		t.Fatalf("session account=%q", body.Account.ID)
	}
	if strings.Contains(response.Body.String(), raw) || strings.Contains(response.Body.String(), hash) {
		t.Fatal("session response leaks the token")
	}
	return cookie, body.CSRF
}

func TestAccountModeRejectsPasswordLinkAndLegacyCookies(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, _, _ := accountWebConfig(t, now)
	cfg.SigningKey = []byte("legacy-key")
	handler := NewWebHandler("", cfg)

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"a","password":"b"}`)))
	if login.Code != http.StatusUnauthorized || len(login.Result().Cookies()) != 0 {
		t.Fatalf("password login status=%d cookies=%d", login.Code, len(login.Result().Cookies()))
	}
	link := httptest.NewRecorder()
	handler.ServeHTTP(link, httptest.NewRequest(http.MethodGet, "/auth/link?token=x", nil))
	if link.Code != http.StatusNotFound && link.Code != http.StatusUnauthorized {
		t.Fatalf("login link status=%d", link.Code)
	}
	// An expiry-only signed cookie from the legacy scheme is not a session.
	legacy := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(&http.Cookie{Name: webSessionCookie, Value: session.SignSession(cfg.SigningKey, now.Add(time.Hour))})
	handler.ServeHTTP(legacy, request)
	if legacy.Code != http.StatusUnauthorized {
		t.Fatalf("legacy cookie status=%d", legacy.Code)
	}
}

func TestAccountSessionExpiresRevokesAndFollowsAccountRemoval(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, accounts := accountWebConfig(t, now)
	clock := now
	cfg.Now = func() time.Time { return clock }
	handler := NewWebHandler("", cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)

	get := func() int {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		request.AddCookie(cookie)
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if get() != http.StatusOK {
		t.Fatal("fresh session rejected")
	}
	clock = now.Add(webSessionTTL)
	if get() != http.StatusUnauthorized {
		t.Fatal("expired session accepted")
	}
	clock = now
	if get() != http.StatusOK {
		t.Fatal("session lost before expiry")
	}

	// Logout revokes the row; the same cookie is dead afterwards.
	logout := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	request.AddCookie(cookie)
	request.Header.Set(csrfHeader, csrf)
	handler.ServeHTTP(logout, request)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	if cookies := logout.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie: %#v", cookies)
	}
	if get() != http.StatusUnauthorized {
		t.Fatal("revoked session accepted")
	}

	// Removing an account ends its sessions on the next request.
	cookie, _ = signIn(t, handler, database, "partner", now)
	delete(accounts.list, "partner")
	if get() != http.StatusUnauthorized {
		t.Fatal("removed account still signed in")
	}
	if n, _ := database.ActiveSessions(context.Background(), "partner", now); n != 0 {
		t.Fatalf("removed account keeps %d sessions", n)
	}
}

func TestAccountModeCookieFlags(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, _, _ := accountWebConfig(t, now)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	if err := issueAccountSession(cfg, response, request, "nigel", now); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	c := cookies[0]
	if !c.Secure || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || !c.Expires.Equal(now.Add(12*time.Hour)) {
		t.Fatalf("cookie flags: %#v", c)
	}
	if len(c.Value) != 43 {
		t.Fatalf("cookie value is not an opaque token: %q", c.Value)
	}
}

func TestMutatingRoutesRequireCSRFAndSameOrigin(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)

	post := func(headers map[string]string) int {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
		request.AddCookie(cookie)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		handler.ServeHTTP(response, request)
		return response.Code
	}
	if code := post(nil); code != http.StatusForbidden {
		t.Fatalf("no csrf header: status=%d", code)
	}
	if code := post(map[string]string{csrfHeader: "wrong"}); code != http.StatusForbidden {
		t.Fatalf("wrong csrf header: status=%d", code)
	}
	if code := post(map[string]string{csrfHeader: csrf, "Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("cross-origin: status=%d", code)
	}
	if code := post(map[string]string{csrfHeader: csrf, "Sec-Fetch-Site": "cross-site"}); code != http.StatusForbidden {
		t.Fatalf("cross-site fetch: status=%d", code)
	}
	// Still signed in after all of that: none of the refused requests ran.
	if code := post(map[string]string{csrfHeader: csrf, "Origin": "https://eggy.example", "Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Fatalf("same-origin with csrf: status=%d", code)
	}
}
