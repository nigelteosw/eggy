package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/google"
	"github.com/nigelteosw/eggy/plugins/auth/grants"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// fakeGoogleLogin records what Begin was given and returns whatever identity
// the test scripted from Complete, checking that the nonce and verifier
// round-tripped through the transaction.
type fakeGoogleLogin struct {
	beganNonce, beganVerifier string
	identity                  google.Identity
	err                       error
	completed                 int
}

func (f *fakeGoogleLogin) Begin(_ context.Context, state, nonce, verifier string) (string, error) {
	f.beganNonce, f.beganVerifier = nonce, verifier
	return "https://accounts.google.test/auth?state=" + url.QueryEscape(state), nil
}

func (f *fakeGoogleLogin) Complete(_ context.Context, code, nonce, verifier string) (google.Identity, error) {
	f.completed++
	if code != "good-code" {
		return google.Identity{}, errors.New("bad code")
	}
	if nonce != f.beganNonce || verifier != f.beganVerifier {
		return google.Identity{}, errors.New("nonce or verifier did not round-trip")
	}
	return f.identity, f.err
}

func googleWebConfig(t *testing.T, now time.Time) (WebUIConfig, *sqlitestore.Store, *fakeAccounts, *fakeGoogleLogin) {
	t.Helper()
	cfg, database, accounts := accountWebConfig(t, now)
	sealer, err := grants.NewSealer("login", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	login := &fakeGoogleLogin{identity: google.Identity{Issuer: "https://accounts.google.com", Subject: "sub-nigel", Email: "nigel@example.com", EmailVerified: true}}
	cfg.GoogleLogin, cfg.Identities, cfg.LoginSealer = login, database, sealer
	return cfg, database, accounts, login
}

// start performs GET /auth/google/start and returns the state Google would
// echo back and the browser-binding cookie.
func start(t *testing.T, handler http.Handler) (string, *http.Cookie) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google/start", nil))
	if response.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", response.Code, response.Body.String())
	}
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == loginCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("login cookie=%#v", cookie)
	}
	return target.Query().Get("state"), cookie
}

func callback(handler http.Handler, query string, cookie *http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback?"+query, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	handler.ServeHTTP(response, request)
	return response
}

func sessionCookie(response *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range response.Result().Cookies() {
		if c.Name == webSessionCookie && c.MaxAge >= 0 && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestGoogleLoginEnrollsAnAllowlistedAccountAndSignsInAgainByBinding(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cfg, database, accounts, login := googleWebConfig(t, now)
	handler := NewWebHandler("", cfg)

	state, browser := start(t, handler)
	response := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("callback status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	cookie := sessionCookie(response)
	if cookie == nil {
		t.Fatal("no session cookie issued")
	}
	if _, _, bound, _ := database.IdentityOf(context.Background(), "nigel"); !bound {
		t.Fatal("first login did not bind the identity")
	}
	sessionResponse := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(sessionResponse, request)
	if sessionResponse.Code != http.StatusOK || !strings.Contains(sessionResponse.Body.String(), `"id":"nigel"`) {
		t.Fatalf("session status=%d body=%s", sessionResponse.Code, sessionResponse.Body.String())
	}

	// The configured address changes, but the binding is what identifies
	// the person: the same subject still signs in as nigel, and the new
	// address alone cannot take the account.
	accounts.list["nigel"] = AccountRecord{ID: "nigel", Email: "renamed@example.com"}
	state, browser = start(t, handler)
	if response := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser); sessionCookie(response) == nil {
		t.Fatal("bound identity could not sign in after an address change")
	}
	login.identity = google.Identity{Issuer: "https://accounts.google.com", Subject: "sub-impostor", Email: "renamed@example.com", EmailVerified: true}
	state, browser = start(t, handler)
	if response := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser); sessionCookie(response) != nil {
		t.Fatal("a new subject presenting the changed address took over a bound account")
	}

	// Reset, then the new subject may enroll.
	if err := database.ResetIdentity(context.Background(), "nigel"); err != nil {
		t.Fatal(err)
	}
	state, browser = start(t, handler)
	if response := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser); sessionCookie(response) == nil {
		t.Fatal("re-enrollment after reset failed")
	}
}

func TestGoogleLoginRefusals(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	cases := map[string]func(t *testing.T, handler http.Handler, login *fakeGoogleLogin, database *sqlitestore.Store) *httptest.ResponseRecorder{
		"unlisted user": func(t *testing.T, handler http.Handler, login *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			login.identity.Email = "stranger@example.com"
			state, browser := start(t, handler)
			return callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser)
		},
		"unverified email": func(t *testing.T, handler http.Handler, login *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			login.identity.EmailVerified = false
			state, browser := start(t, handler)
			return callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser)
		},
		"provider denied": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			state, browser := start(t, handler)
			return callback(handler, "state="+url.QueryEscape(state)+"&error=access_denied", browser)
		},
		"bad code": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			state, browser := start(t, handler)
			return callback(handler, "state="+url.QueryEscape(state)+"&code=stolen", browser)
		},
		"missing state": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			_, browser := start(t, handler)
			return callback(handler, "code=good-code", browser)
		},
		"forged state": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			_, browser := start(t, handler)
			return callback(handler, "state=forged&code=good-code", browser)
		},
		"different browser": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			state, _ := start(t, handler)
			_, other := start(t, handler)
			return callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", other)
		},
		"replay": func(t *testing.T, handler http.Handler, _ *fakeGoogleLogin, _ *sqlitestore.Store) *httptest.ResponseRecorder {
			state, browser := start(t, handler)
			if first := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser); sessionCookie(first) == nil {
				t.Fatal("first use failed")
			}
			return callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser)
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, database, _, login := googleWebConfig(t, now)
			handler := NewWebHandler("", cfg)
			response := run(t, handler, login, database)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != loginFailedURL {
				t.Fatalf("status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
			}
			if sessionCookie(response) != nil {
				t.Fatal("a refused login issued a session")
			}
			if strings.Contains(response.Body.String(), "good-code") {
				t.Fatal("response echoed the code")
			}
		})
	}
	// A subject already bound to partner is partner, whatever address the
	// token carries: the binding decides, and nigel's allowlisted address
	// cannot claim it.
	cfg, database, _, login := googleWebConfig(t, now)
	if err := database.BindIdentity(context.Background(), "partner", login.identity.Issuer, login.identity.Subject); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler("", cfg)
	state, browser := start(t, handler)
	// partner's identity signs in as partner, not as nigel, whatever the
	// address says.
	response := callback(handler, "state="+url.QueryEscape(state)+"&code=good-code", browser)
	cookie := sessionCookie(response)
	if cookie == nil {
		t.Fatal("bound partner could not sign in")
	}
	sessionResponse := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(sessionResponse, request)
	if !strings.Contains(sessionResponse.Body.String(), `"id":"partner"`) {
		t.Fatalf("session body=%s", sessionResponse.Body.String())
	}
}

func TestGoogleLoginRoutesAbsentInLegacyMode(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google/start", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}
