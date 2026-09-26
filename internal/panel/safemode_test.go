package panel

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSafeMode(t *testing.T, configPath string, repaired *bool) http.Handler {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return NewSafeModeHandler(SafeMode{
		ConfigPath: configPath,
		Failure:    errors.New("decode config: field scheduler not found"),
		Getenv: func(name string) string {
			return map[string]string{"DEEPSEEK_API_KEY": "key", "TELEGRAM_BOT_TOKEN": "token", "TELEGRAM_WEBHOOK_SECRET": "secret", "GITHUB_TOKEN": "gh"}[name]
		},
		Repaired: func() { *repaired = true },
		Web:      testWebConfig(t, now),
	})
}

// safeModeConfigPath writes a config that does not load, which is the
// situation safe mode exists for.
func safeModeConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()+"scheduler:\n  heartbeat_cadence: 3h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func safeModeCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login did not set a session cookie: %s", login.Body.String())
	}
	return cookies[0]
}

// Safe mode serves the same web app as always and tells it, unauthenticated,
// which screen to render. Without that probe the app would have to infer safe
// mode from a sequence of failing requests.
func TestSafeModeServesTheAppAndAnnouncesItself(t *testing.T) {
	repaired := false
	handler := testSafeMode(t, safeModeConfigPath(t), &repaired)

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("app shell status=%d", page.Code)
	}

	mode := httptest.NewRecorder()
	handler.ServeHTTP(mode, httptest.NewRequest(http.MethodGet, "/api/mode", nil))
	if mode.Code != http.StatusOK || !strings.Contains(mode.Body.String(), `"safe"`) {
		t.Fatalf("mode status=%d body=%s", mode.Code, mode.Body.String())
	}
}

// The same probe on a running Eggy says the opposite, so the app can tell the
// two apart on one request.
func TestNormalModeAnnouncesItself(t *testing.T) {
	handler := NewWebHandler("", testWebConfig(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mode", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"normal"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The startup error and the config are the two things safe mode exposes, and
// both are behind the owner's session: the error text can name paths and
// settings, and an unauthenticated reader has no business with either.
func TestSafeModeRequiresSessionForFailureAndConfig(t *testing.T) {
	repaired := false
	path := safeModeConfigPath(t)
	handler := testSafeMode(t, path, &repaired)

	for _, route := range []string{"/api/safemode", "/api/config/raw"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d, want 401", route, response.Code)
		}
		if strings.Contains(response.Body.String(), "scheduler") {
			t.Fatalf("%s leaked the failure to an anonymous caller: %s", route, response.Body.String())
		}
	}

	cookie := safeModeCookie(t, handler)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/safemode", nil)
	attachSession(request, cookie)
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "field scheduler not found") {
		t.Fatalf("failure not reported to the owner: %s", response.Body.String())
	}

	configResponse := httptest.NewRecorder()
	configRequest := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	attachSession(configRequest, cookie)
	handler.ServeHTTP(configResponse, configRequest)
	if !strings.Contains(configResponse.Body.String(), "heartbeat_cadence") {
		t.Fatalf("config not served verbatim: %s", configResponse.Body.String())
	}
}

func TestSafeModeSavesRepairedConfigAndSignalsRestart(t *testing.T) {
	repaired := false
	path := safeModeConfigPath(t)
	handler := testSafeMode(t, path, &repaired)
	cookie := safeModeCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/config/raw", strings.NewReader(validConfig()))
	attachSession(request, cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !repaired {
		t.Fatal("a config that loads did not signal the supervisor to retry startup")
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "heartbeat_cadence") {
		t.Fatalf("stored config was not replaced:\n%s", stored)
	}
}

// A config that would not start is refused, and the file on disk is left
// exactly as it was -- safe mode must not be able to make things worse.
func TestSafeModeRejectsConfigThatWouldNotStart(t *testing.T) {
	repaired := false
	path := safeModeConfigPath(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := testSafeMode(t, path, &repaired)
	cookie := safeModeCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/config/raw", strings.NewReader("server:\n  listen: ':8080'\nnonsense: true\n"))
	attachSession(request, cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if repaired {
		t.Fatal("a config that does not load signalled a restart")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("rejected config changed the stored file:\n%s", after)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-candidate-") {
			t.Fatalf("validation left a staged file behind: %s", entry.Name())
		}
	}
}

// Every other route belongs to the Eggy that is not running. Unavailable says
// so; a 404 would read as "this deployment does not have that feature".
func TestSafeModeReportsOtherRoutesUnavailable(t *testing.T) {
	repaired := false
	handler := testSafeMode(t, safeModeConfigPath(t), &repaired)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/chat/threads", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// Safe mode has no fallback: with the database and identity unavailable
// nobody gets in, and the routes say so rather than pretending a login
// exists. With them, the ordinary password login works against the
// existing sessions -- a local password included, so recovery does not
// depend on the environment credentials alone.
func TestSafeModeWithoutIdentityHasNoFallbackAndWithItUsesTheNormalLogin(t *testing.T) {
	now := time.Now().UTC()
	unavailable := NewSafeModeHandler(SafeMode{ConfigPath: safeModeConfigPath(t), Failure: errors.New("boom"), Web: WebUIConfig{EnvironmentAlias: "owner@example.com", EnvironmentPasswordHash: environmentHash(t, "hunter2")}})
	login := httptest.NewRecorder()
	unavailable.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"owner@example.com","password":"hunter2"}`)))
	if login.Code != http.StatusUnauthorized || len(login.Result().Cookies()) != 0 {
		t.Fatalf("login without identity: status=%d", login.Code)
	}
	mode := httptest.NewRecorder()
	unavailable.ServeHTTP(mode, httptest.NewRequest(http.MethodGet, "/api/mode", nil))
	if !strings.Contains(mode.Body.String(), `"login":"unavailable"`) {
		t.Fatalf("mode=%s", mode.Body.String())
	}

	cfg, database, _ := accountWebConfig(t, now)
	localPassword(t, database, "partner", "partner-password-long")
	handler := NewSafeModeHandler(SafeMode{ConfigPath: safeModeConfigPath(t), Failure: errors.New("boom"), Web: cfg})
	unauthed := httptest.NewRecorder()
	handler.ServeHTTP(unauthed, httptest.NewRequest(http.MethodGet, "/api/safemode", nil))
	if unauthed.Code != http.StatusUnauthorized {
		t.Fatalf("safemode without session: status=%d", unauthed.Code)
	}
	for _, credentials := range []string{`{"username":"owner@example.com","password":"hunter2"}`, `{"username":"partner","password":"partner-password-long"}`} {
		login := httptest.NewRecorder()
		handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(credentials)))
		if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
			t.Fatalf("login %s: status=%d body=%s", credentials, login.Code, login.Body.String())
		}
		cookie := login.Result().Cookies()[0]
		authed := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/safemode", nil)
		attachSession(request, cookie)
		handler.ServeHTTP(authed, request)
		if authed.Code == http.StatusUnauthorized || !strings.Contains(authed.Body.String(), "boom") {
			t.Fatalf("safemode with session: status=%d body=%s", authed.Code, authed.Body.String())
		}
		raw := httptest.NewRecorder()
		rawRequest := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
		attachSession(rawRequest, cookie)
		handler.ServeHTTP(raw, rawRequest)
		if raw.Code != http.StatusOK {
			t.Fatalf("raw config with session: status=%d", raw.Code)
		}
	}
	wrong := httptest.NewRecorder()
	handler.ServeHTTP(wrong, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"partner","password":"hunter2"}`)))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("someone else's password: status=%d", wrong.Code)
	}
}
