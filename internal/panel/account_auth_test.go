package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLocalPasswordIssuesAccountSession(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	cfg.Auth = db
	encoded, err := session.HashPassword("partner-password-long")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := db.AccountAuth(context.Background(), "partner")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountPassword(context.Background(), "partner", encoded, auth.Generation); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler("", cfg)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"partner","password":"partner-password-long"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	got, err := db.SessionAccount(context.Background(), session.HashToken(cookies[0].Value), now)
	if err != nil || got != "partner" {
		t.Fatalf("account=%q err=%v", got, err)
	}
	if strings.Contains(response.Body.String(), "pbkdf2") || strings.Contains(response.Body.String(), "partner-password-long") {
		t.Fatal("response echoes a credential")
	}
}

// pausingAuth hands back the credential record and then blocks until the
// test releases it, which is the window a reset races into.
type pausingAuth struct {
	ports.AccountAuthStore
	loaded  chan struct{}
	release chan struct{}
	once    bool
}

func (p *pausingAuth) AccountAuth(ctx context.Context, id string) (ports.AccountAuth, error) {
	record, err := p.AccountAuthStore.AccountAuth(ctx, id)
	if !p.once {
		p.once = true
		close(p.loaded)
		<-p.release
	}
	return record, err
}

func TestPasswordResetDuringVerificationDeniesTheStaleLogin(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	localPassword(t, db, "partner", "partner-password-long")
	pausing := &pausingAuth{AccountAuthStore: db, loaded: make(chan struct{}), release: make(chan struct{})}
	cfg.Auth = pausing
	handler := NewWebHandler("", cfg)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"partner","password":"partner-password-long"}`)))
		done <- response
	}()
	<-pausing.loaded
	// The reset lands on the real store while the login holds the old
	// generation.
	localPassword(t, db, "partner", "partner-password-newer")
	close(pausing.release)
	response := <-done
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatalf("stale login status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
	}
	if n, _ := db.ActiveSessions(context.Background(), "partner", now); n != 0 {
		t.Fatalf("stale login created %d sessions", n)
	}
}

func TestPasswordLoginRefusals(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, accounts := accountWebConfig(t, now)
	localPassword(t, db, "partner", "partner-password-long")
	handler := NewWebHandler("", cfg)
	login := func(body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
		request.RemoteAddr = "10.1.1." + string(rune('1'+len(body)%9)) + ":1"
		handler.ServeHTTP(response, request)
		return response
	}
	cases := map[string]struct {
		body string
		code int
	}{
		"environment alias":         {`{"username":"OWNER@example.com","password":"hunter2"}`, http.StatusOK},
		"environment account by id": {`{"username":"nigel","password":"hunter2"}`, http.StatusOK},
		"retired email field":       {`{"email":"owner@example.com","password":"hunter2"}`, http.StatusBadRequest},
		"email beside username":     {`{"username":"nigel","email":"owner@example.com","password":"hunter2"}`, http.StatusBadRequest},
		"local password":            {`{"username":"partner","password":"partner-password-long"}`, http.StatusOK},
		"local password for env":    {`{"username":"nigel","password":"partner-password-long"}`, http.StatusUnauthorized},
		"env password for local":    {`{"username":"partner","password":"hunter2"}`, http.StatusUnauthorized},
		"unknown user":              {`{"username":"stranger","password":"partner-password-long"}`, http.StatusUnauthorized},
		"case-changed id":           {`{"username":"Partner","password":"partner-password-long"}`, http.StatusUnauthorized},
		"empty password":            {`{"username":"partner","password":""}`, http.StatusBadRequest},
		"overlong password":         {`{"username":"partner","password":"` + strings.Repeat("x", 257) + `"}`, http.StatusBadRequest},
		"unknown field":             {`{"username":"partner","password":"partner-password-long","remember":true}`, http.StatusBadRequest},
		"oversized body":            {`{"username":"partner","password":"x","pad":"` + strings.Repeat("y", authBodyLimit) + `"}`, http.StatusBadRequest},
		"not json":                  {`username=partner`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := login(tc.body)
			if response.Code != tc.code {
				t.Fatalf("status=%d want %d body=%s", response.Code, tc.code, response.Body.String())
			}
			if (response.Code == http.StatusOK) != (len(response.Result().Cookies()) == 1) {
				t.Fatalf("cookies=%d for status %d", len(response.Result().Cookies()), response.Code)
			}
		})
	}
	// A removed account is refused even with the right password, and its
	// pending replacement (a row without a password) too.
	delete(accounts.list, "partner")
	if response := login(`{"username":"partner","password":"partner-password-long"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("removed account status=%d", response.Code)
	}
	accounts.list["pending"] = AccountRecord{ID: "pending"}
	if err := db.RegisterAccountAuth(context.Background(), "pending"); err != nil {
		t.Fatal(err)
	}
	if response := login(`{"username":"pending","password":"partner-password-long"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("pending account status=%d", response.Code)
	}
	// Unsupported methods are 405.
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest("GET", "/api/login", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET login status=%d", get.Code)
	}
}

func TestEnvironmentLoginRefusesARebindingUntilRestart(t *testing.T) {
	now := time.Now().UTC()
	cfg, _, accounts := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	// The config now binds partner, but this process bound nigel at boot.
	accounts.passwordAccount = "partner"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 0 {
		t.Fatalf("rebound environment login status=%d", response.Code)
	}
}

func TestPasswordVerificationIsBoundedToTwoSlots(t *testing.T) {
	now := time.Now().UTC()
	cfg, _, _ := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	limiter := newVerifyLimiter()
	limiter.acquire()
	limiter.acquire()
	// Reach into the same limiter shape the handler uses to prove the
	// refusal path, rather than racing real verifications.
	route := handlePasswordLogin("", cfg, session.NewLoginThrottle(func() time.Time { return now }), limiter, dummyPasswordHash(), func() time.Time { return now })
	response := httptest.NewRecorder()
	route(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("saturated status=%d", response.Code)
	}
	limiter.release()
	response = httptest.NewRecorder()
	route(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("after release status=%d body=%s", response.Code, response.Body.String())
	}
	_ = handler
}

func TestSessionGuardRefusesARetiredRowEvenWhenYAMLStillListsIt(t *testing.T) {
	now := time.Now().UTC()
	cfg, db, _ := accountWebConfig(t, now)
	handler := NewWebHandler("", cfg)
	cookie, _ := signIn(t, handler, db, "partner", now)
	if err := db.RetireAccountAuth(context.Background(), "partner"); err != nil {
		t.Fatal(err)
	}
	// Directory still lists partner; the retired row wins.
	request := httptest.NewRequest("GET", "/api/session", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("retired row status=%d", response.Code)
	}
}
