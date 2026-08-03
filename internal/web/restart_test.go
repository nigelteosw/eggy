package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeRestarter struct{ restarts int }

func (f *fakeRestarter) Restart() { f.restarts++ }

// restartEnv stands in for the daemon's own lookup: the pre-flight loads the
// config exactly as startup would, so every variable validConfig names has to
// resolve or the load fails for the wrong reason.
func restartEnv(key string) string {
	switch key {
	case "DEEPSEEK_API_KEY", "TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET", "GITHUB_TOKEN":
		return "test-value"
	}
	return ""
}

// The button and /restart are one authority: both go through
// commands.Restart, so the panel cannot restart under conditions chat would
// have refused.
func TestRestartRouteRebuildsTheDaemon(t *testing.T) {
	restarter := &fakeRestarter{}
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Restarter = restarter
	webConfig.Getenv = restartEnv
	handler := NewWebHandler(writeConfigFile(t, validConfig()), webConfig)
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if restarter.restarts != 1 {
		t.Fatalf("restarts=%d", restarter.restarts)
	}
}

// The pre-flight is what makes the button safe to offer at all: a config that
// would not load takes the whole daemon into safe mode.
func TestRestartRouteRefusesAConfigThatWouldNotLoad(t *testing.T) {
	restarter := &fakeRestarter{}
	path := writeConfigFile(t, validConfig())
	if err := os.WriteFile(path, []byte("agent:\n  default_model: missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Restarter = restarter
	webConfig.Getenv = restartEnv
	handler := NewWebHandler(path, webConfig)
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if restarter.restarts != 0 {
		t.Fatalf("restarted into a broken config: restarts=%d", restarter.restarts)
	}
	if !strings.Contains(response.Body.String(), "Not restarting") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

// Every route behind the session is owner-only, and a restart is the most
// disruptive of them.
func TestRestartRouteRequiresASession(t *testing.T) {
	restarter := &fakeRestarter{}
	webConfig := testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	webConfig.Restarter = restarter
	handler := NewWebHandler(writeConfigFile(t, validConfig()), webConfig)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/restart", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if restarter.restarts != 0 {
		t.Fatalf("an unauthenticated request restarted Eggy: restarts=%d", restarter.restarts)
	}
}
