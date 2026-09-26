package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// accountConfigYAML is validConfig converted to two accounts, nigel bound
// to the environment login.
func accountConfigYAML() string {
	return strings.Replace(validConfig(), "telegram:\n  owner_id: 42\n", `accounts:
  - id: nigel
    telegram_user_id: 42
  - id: partner
web:
  password_account_id: nigel
`, 1)
}

// authenticatedJSON is one signed-in JSON request through the handler.
func authenticatedJSON(h http.Handler, cookie *http.Cookie, csrf, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Eggy-CSRF", csrf)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var loginClients atomic.Int64

// passwordLogin posts one login. Each call comes from its own client
// address so the throttle, which has its own tests, does not turn the
// fifth deliberate failure of a lifecycle test into a 429.
func passwordLogin(h http.Handler, username, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0." + strconv.FormatInt(loginClients.Add(1)%250+1, 10) + ":" + strconv.FormatInt(loginClients.Load()+1000, 10)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func accountsList(t *testing.T, h http.Handler, cookie *http.Cookie, csrf string) accountsView {
	t.Helper()
	response := authenticatedJSON(h, cookie, csrf, http.MethodGet, "/api/config/accounts", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var view accountsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestAccountLifecycleCreateResetRemoveAndNeverReuse(t *testing.T) {
	// Real time: the store prunes expired sessions by the clock, and a
	// fixture clock in the past would see every session as expired.
	now := time.Now().UTC()
	cfg, database, accounts := accountWebConfig(t, now)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	// The directory follows the file, as bootstrap's does.
	accounts.list = nil
	cfg.Accounts = fileDirectory{path: path}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "partner", now)

	view := accountsList(t, handler, cookie, csrf)
	if !view.AccountMode || len(view.Accounts) != 2 || view.PasswordAccountID != "nigel" || view.EnvironmentAlias != "owner@example.com" {
		t.Fatalf("view=%+v", view)
	}
	if view.Accounts[0].PasswordState != passwordStateEnvironment || !view.Accounts[0].WebLinkAvailable || view.Accounts[0].Self {
		t.Fatalf("nigel row=%+v", view.Accounts[0])
	}
	if view.Accounts[1].PasswordState != passwordStatePending || view.Accounts[1].WebLinkAvailable || !view.Accounts[1].Self || !view.Accounts[1].SignedIn {
		t.Fatalf("partner row=%+v", view.Accounts[1])
	}

	// Create third with a password and a Telegram ID.
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"third","telegram_user_id":7,"password":"third-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", response.Code, response.Body.String())
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"fourth","telegram_user_id":8,"password":"short"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("short password status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"fourth","telegram_user_id":7,"password":"fourth-password-long"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate sender status=%d", response.Code)
	}
	view = accountsList(t, handler, cookie, csrf)
	if len(view.Accounts) != 3 || view.Accounts[2].PasswordState != passwordStateSet || !view.Accounts[2].WebLinkAvailable {
		t.Fatalf("after add: %+v", view.Accounts)
	}
	if body := authenticatedJSON(handler, cookie, csrf, http.MethodGet, "/api/config/accounts", "").Body.String(); strings.Contains(body, "third-password-long") || strings.Contains(body, "pbkdf2") {
		t.Fatal("the list echoes a credential")
	}

	// third signs in with the password and holds an unused /web link.
	thirdLogin := passwordLogin(handler, "third", "third-password-long")
	if thirdLogin.Code != http.StatusOK || len(thirdLogin.Result().Cookies()) != 1 {
		t.Fatalf("third login status=%d body=%s", thirdLogin.Code, thirdLogin.Body.String())
	}
	thirdCookie := thirdLogin.Result().Cookies()[0]
	thirdAuth, err := database.AccountAuth(context.Background(), "third")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateWebLoginLink(context.Background(), "third-link", ports.WebLoginLink{AccountID: "third", SenderID: "7", Generation: thirdAuth.Generation, ExpiresAt: now.Add(5 * time.Minute)}, now); err != nil {
		t.Fatal(err)
	}
	probe := func(c *http.Cookie) int {
		r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if probe(thirdCookie) != http.StatusOK {
		t.Fatal("third's fresh session rejected")
	}

	// A self change needs the current password; a bypass attempt is refused.
	thirdCSRF := csrfToken(session.HashToken(thirdCookie.Value))
	if response := authenticatedJSON(handler, thirdCookie, thirdCSRF, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-newer"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("self change without current status=%d body=%s", response.Code, response.Body.String())
	}
	if response := authenticatedJSON(handler, thirdCookie, thirdCSRF, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-newer","current_password":"wrong-current-password"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("self change wrong current status=%d body=%s", response.Code, response.Body.String())
	}
	if probe(thirdCookie) != http.StatusOK {
		t.Fatal("a refused change ended the session")
	}

	// partner resets third's password without the current one: old cookie
	// and unused link die, partner stays signed in.
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-reset"}`); response.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", response.Code, response.Body.String())
	}
	if probe(thirdCookie) != http.StatusUnauthorized {
		t.Fatal("third's old session survived the reset")
	}
	if _, err := database.WebLoginLink(context.Background(), "third-link", now); !errors.Is(err, ports.ErrAuthDenied) {
		t.Fatalf("third's unused link survived the reset: %v", err)
	}
	if probe(cookie) != http.StatusOK {
		t.Fatal("partner was signed out by resetting someone else")
	}
	if passwordLogin(handler, "third", "third-password-long").Code != http.StatusUnauthorized {
		t.Fatal("old password still works")
	}
	if passwordLogin(handler, "third", "third-password-reset").Code != http.StatusOK {
		t.Fatal("new password does not work")
	}

	// The environment account has no local password; its sessions can
	// still be revoked.
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/nigel/password", `{"password":"nigel-password-long"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("environment reset status=%d body=%s", response.Code, response.Body.String())
	}
	nigelCookie, _ := signIn(t, handler, database, "nigel", now)
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/nigel/revoke-sessions", `{}`); response.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
	if probe(nigelCookie) != http.StatusUnauthorized {
		t.Fatal("nigel's session survived revocation")
	}

	// Own change: the current password is verified, and the caller is
	// signed out.
	thirdLogin = passwordLogin(handler, "third", "third-password-reset")
	thirdCookie = thirdLogin.Result().Cookies()[0]
	thirdCSRF = csrfToken(session.HashToken(thirdCookie.Value))
	response := authenticatedJSON(handler, thirdCookie, thirdCSRF, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-final","current_password":"third-password-reset"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("own change status=%d body=%s", response.Code, response.Body.String())
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("own change must clear the cookie: %#v", cookies)
	}
	if probe(thirdCookie) != http.StatusUnauthorized {
		t.Fatal("own change left the session alive")
	}

	// Removal: self is refused, the bound account is refused, third goes
	// and its ID never comes back.
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodDelete, "/api/config/accounts/partner", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("remove self status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodDelete, "/api/config/accounts/nigel", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("remove bound status=%d", response.Code)
	}
	if err := database.StartTrace(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "third"}), ports.Trace{ID: "third-trace", ConversationID: "web", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodDelete, "/api/config/accounts/third", ""); response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if passwordLogin(handler, "third", "third-password-final").Code != http.StatusUnauthorized {
		t.Fatal("removed account can still log in")
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"THIRD","telegram_user_id":9,"password":"another-password-long"}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "cannot be reissued") {
		t.Fatalf("recreate retired id status=%d body=%s", response.Code, response.Body.String())
	}
	if view := accountsList(t, handler, cookie, csrf); len(view.Accounts) != 2 {
		t.Fatalf("retired id was written: %+v", view.Accounts)
	}
	traces, err := database.ListTraces(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "third"}), 10)
	if err != nil || len(traces) != 1 {
		t.Fatalf("old trace ownership changed: %+v err=%v", traces, err)
	}
	if traces, _ := database.ListTraces(ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "THIRD"}), 10); len(traces) != 0 {
		t.Fatal("a differently cased id reads the retired account's traces")
	}

	// Expected email still writes through; the written file loads.
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/google/expected-email", `{"email":"Eggy@Example.com"}`); response.Code != http.StatusOK {
		t.Fatalf("expected email status=%d body=%s", response.Code, response.Body.String())
	}
	if _, _, err := config.LoadConfig(path, func(key string) string {
		return map[string]string{"DEEPSEEK_API_KEY": "k", "GITHUB_TOKEN": "g", "TELEGRAM_BOT_TOKEN": "t", "TELEGRAM_WEBHOOK_SECRET": "s", "EGGY_ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "EGGY_UI_USER_EMAIL": "owner@example.com", "EGGY_UI_PASSWORD": "hunter2"}[key]
	}); err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
}

// fileDirectory reads membership from the config file, as bootstrap's
// directory does, so the routes that write YAML are observed by the guard.
type fileDirectory struct{ path string }

func (d fileDirectory) current() (config.Config, bool) {
	cfg, err := config.LoadDocument(d.path)
	if err != nil || cfg.Validate() != nil {
		return config.Config{}, false
	}
	return cfg, true
}

func (d fileDirectory) Account(id string) (AccountRecord, bool) {
	cfg, ok := d.current()
	if !ok {
		return AccountRecord{}, false
	}
	account, ok := cfg.Account(id)
	return AccountRecord{ID: account.ID, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID}, ok
}

func (d fileDirectory) Accounts() []AccountRecord {
	cfg, ok := d.current()
	if !ok {
		return nil
	}
	var records []AccountRecord
	for _, account := range cfg.Principals() {
		records = append(records, AccountRecord{ID: account.ID, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID})
	}
	return records
}

func (d fileDirectory) PasswordAccountID() string {
	cfg, _ := d.current()
	return cfg.PasswordAccountID()
}

func (d fileDirectory) TelegramEnabled() bool {
	cfg, ok := d.current()
	return ok && cfg.TelegramEnabled()
}

func TestAccountRoutesRefuseMissingCSRFAndKeepCredentialsOutOfErrors(t *testing.T) {
	now := time.Now().UTC()
	cfg, database, _ := accountWebConfig(t, now)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	for _, wrong := range []string{"", "wrong"} {
		if response := authenticatedJSON(handler, cookie, wrong, http.MethodPost, "/api/config/accounts", `{"id":"third","password":"third-password-long"}`); response.Code != http.StatusForbidden {
			t.Fatalf("csrf %q status=%d", wrong, response.Code)
		}
	}
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, "/api/config/accounts/nigel/password", strings.NewReader(`{"password":"anonymous-password-long"}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous reset status=%d", anonymous.Code)
	}
	// Body bound and strictness.
	huge := `{"id":"third","password":"` + strings.Repeat("x", authBodyLimit) + `"}`
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", huge); response.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"third","password":"third-password-long","role":"admin"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/partner/password", `{"password":"short"}`); response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "short\"") {
		t.Fatalf("short reset status=%d body=%s", response.Code, response.Body.String())
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/stranger/password", `{"password":"stranger-password-long"}`); response.Code != http.StatusNotFound {
		t.Fatalf("unknown target status=%d", response.Code)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/partner/revoke-sessions", `{"extra":1}`); response.Code != http.StatusBadRequest {
		t.Fatalf("revoke with body status=%d", response.Code)
	}
}

func TestAccountCreationReportsAPendingCredentialWhenTheStoreFails(t *testing.T) {
	now := time.Now().UTC()
	cfg, database, _ := accountWebConfig(t, now)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	failing := &failingAuth{AccountAuthStore: database}
	cfg.Auth = failing
	cfg.Accounts = fileDirectory{path: path}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	failing.failRegister = true
	response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts", `{"id":"third","password":"third-password-long"}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		AccountCreated bool   `json:"account_created"`
		PasswordState  string `json:"password_state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.AccountCreated || body.PasswordState != passwordStatePending {
		t.Fatalf("body=%s err=%v", response.Body.String(), err)
	}
	if strings.Contains(response.Body.String(), "third-password-long") {
		t.Fatal("the failure echoes the password")
	}
	doc, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Account("third"); !ok {
		t.Fatal("membership was not retained")
	}
	if passwordLogin(handler, "third", "third-password-long").Code != http.StatusUnauthorized {
		t.Fatal("a pending account logged in")
	}
	// The retry is setting the password on the listed pending account.
	failing.failRegister = false
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-long"}`); response.Code != http.StatusNotFound {
		// No row yet: registration is bootstrap's job at the next start,
		// so the route reports not found rather than inventing a row.
		t.Fatalf("pending reset status=%d body=%s", response.Code, response.Body.String())
	}
	if err := database.RegisterAccountAuth(context.Background(), "third"); err != nil {
		t.Fatal(err)
	}
	if response := authenticatedJSON(handler, cookie, csrf, http.MethodPost, "/api/config/accounts/third/password", `{"password":"third-password-long"}`); response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	if passwordLogin(handler, "third", "third-password-long").Code != http.StatusOK {
		t.Fatal("retried password does not work")
	}

	// Removal whose cleanup fails still locks the person out.
	failing.failRetire = true
	response = authenticatedJSON(handler, cookie, csrf, http.MethodDelete, "/api/config/accounts/third", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("remove with failing cleanup status=%d body=%s", response.Code, response.Body.String())
	}
	if _, ok := config.Config.Account(func() config.Config { doc, _ := config.LoadDocument(path); return doc }(), "third"); ok {
		t.Fatal("membership survived the removal")
	}
	if passwordLogin(handler, "third", "third-password-long").Code != http.StatusUnauthorized {
		t.Fatal("removed account logged in before cleanup")
	}
}

// failingAuth wraps the real store and fails selected operations.
type failingAuth struct {
	ports.AccountAuthStore
	failRegister bool
	failRetire   bool
}

func (f *failingAuth) RegisterAccountAuth(ctx context.Context, id string) error {
	if f.failRegister {
		return errors.New("store unavailable")
	}
	return f.AccountAuthStore.RegisterAccountAuth(ctx, id)
}

func (f *failingAuth) RetireAccountAuth(ctx context.Context, id string) error {
	if f.failRetire {
		return errors.New("store unavailable")
	}
	return f.AccountAuthStore.RetireAccountAuth(ctx, id)
}

func TestTelegramEnableRouteRequiresBotEnvironmentAndWritesExplicitState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	missing := telegramEnabledRoute(path, WebUIConfig{Getenv: func(string) string { return "" }})
	response := httptest.NewRecorder()
	missing(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"enabled":true}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("missing secrets changed config")
	}
	ready := telegramEnabledRoute(path, WebUIConfig{Getenv: func(name string) string {
		return map[string]string{"TELEGRAM_BOT_TOKEN": "t", "TELEGRAM_WEBHOOK_SECRET": "s"}[name]
	}})
	response = httptest.NewRecorder()
	ready(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"enabled":true}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cfg, err := config.LoadDocument(path)
	if err != nil || cfg.Telegram.Enabled == nil || !*cfg.Telegram.Enabled {
		t.Fatalf("telegram=%#v err=%v", cfg.Telegram, err)
	}
}

func TestAccountsCardConvertsALegacyDeployment(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testWebConfig(t, now)
	handler := NewWebHandler(path, cfg)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	cookie := login.Result().Cookies()[0]
	do := func(method, target, body string) *httptest.ResponseRecorder {
		return authenticatedJSON(handler, cookie, csrfToken(session.HashToken(cookie.Value)), method, target, body)
	}
	response := do(http.MethodGet, "/api/config/accounts", "")
	var view accountsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.AccountMode || view.LegacyOwner != "42" || view.LegacyTelegramID != 42 {
		t.Fatalf("legacy view=%+v", view)
	}
	if response := do(http.MethodPost, "/api/config/accounts", `{"id":"x","password":"x-password-long-enough"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("add in legacy mode status=%d", response.Code)
	}
	body := `{"accounts":[{"id":"nigel","telegram_user_id":42},{"id":"partner"}],"migration_owner_id":"nigel","password_account_id":"nigel"}`
	if response := do(http.MethodPost, "/api/config/accounts/convert", body); response.Code != http.StatusOK {
		t.Fatalf("convert status=%d body=%s", response.Code, response.Body.String())
	}
	doc, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.AccountMode() || doc.MigrationOwnerID != "nigel" || doc.Owner.ID != "" || doc.PasswordAccountID() != "nigel" {
		t.Fatalf("converted=%+v", doc)
	}
	// Both accounts got credential rows so /web works before any password.
	for _, id := range []string{"nigel", "partner"} {
		if record, err := cfg.Auth.AccountAuth(context.Background(), id); err != nil || record.Retired {
			t.Fatalf("row %s=%+v err=%v", id, record, err)
		}
	}
}
