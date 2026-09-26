package panel

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

func setupRequest(handler http.Handler, method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	handler.ServeHTTP(response, request)
	return response
}

func testSetupMode(now time.Time, token string, complete func(config.SetupInput) error) SetupMode {
	return SetupMode{
		PublicBaseURL: "https://eggy.example",
		TokenHash:     sha256.Sum256([]byte(token)),
		SessionKey:    []byte("01234567890123456789012345678901"),
		Expires:       now.Add(30 * time.Minute),
		Now:           func() time.Time { return now },
		Complete:      complete,
	}
}

func exchangeSetup(t *testing.T, handler http.Handler, token string) *http.Cookie {
	t.Helper()
	response := setupRequest(handler, http.MethodPost, "/api/setup/session", `{"token":"`+token+`"}`, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("exchange status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%#v", cookies)
	}
	return cookies[0]
}

func TestSetupModeExposesOnlySetupRoutes(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	handler := NewSetupModeHandler(testSetupMode(now, "known-token", func(config.SetupInput) error { return nil }))
	mode := setupRequest(handler, http.MethodGet, "/api/mode", "", nil)
	if mode.Code != http.StatusOK || !strings.Contains(mode.Body.String(), `"mode":"setup"`) {
		t.Fatalf("mode status=%d body=%s", mode.Code, mode.Body.String())
	}
	unavailable := setupRequest(handler, http.MethodGet, "/api/chat/threads", "", nil)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("normal route status=%d", unavailable.Code)
	}
}

func TestSetupTokenIsExchangedOnceAndNeverReturned(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	handler := NewSetupModeHandler(testSetupMode(now, "known-token", func(config.SetupInput) error { return nil }))
	first := setupRequest(handler, http.MethodPost, "/api/setup/session", `{"token":"known-token"}`, nil)
	if first.Code != http.StatusNoContent || strings.Contains(first.Body.String(), "known-token") {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || !cookie.Secure {
		t.Fatalf("cookie = %#v", cookie)
	}
	replay := setupRequest(handler, http.MethodPost, "/api/setup/session", `{"token":"known-token"}`, nil)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d", replay.Code)
	}
}

func TestSetupWrongAndExpiredTokensAreIndistinguishable(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	wrong := NewSetupModeHandler(testSetupMode(now, "right", func(config.SetupInput) error { return nil }))
	wrongResponse := setupRequest(wrong, http.MethodPost, "/api/setup/session", `{"token":"wrong"}`, nil)
	expiredMode := testSetupMode(now, "right", func(config.SetupInput) error { return nil })
	expiredMode.Expires = now.Add(-time.Second)
	expired := NewSetupModeHandler(expiredMode)
	expiredResponse := setupRequest(expired, http.MethodPost, "/api/setup/session", `{"token":"right"}`, nil)
	if wrongResponse.Code != http.StatusUnauthorized || expiredResponse.Code != http.StatusUnauthorized || wrongResponse.Body.String() != expiredResponse.Body.String() {
		t.Fatalf("wrong=(%d,%q) expired=(%d,%q)", wrongResponse.Code, wrongResponse.Body.String(), expiredResponse.Code, expiredResponse.Body.String())
	}
}

func TestSetupCompletionRequiresSessionAndRejectsUnknownFields(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	called := false
	handler := NewSetupModeHandler(testSetupMode(now, "known-token", func(config.SetupInput) error { called = true; return nil }))
	body := `{"account_id":"you"}`
	if response := setupRequest(handler, http.MethodPost, "/api/setup/complete", body, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.Code)
	}
	cookie := exchangeSetup(t, handler, "known-token")
	unknown := setupRequest(handler, http.MethodPost, "/api/setup/complete", `{"account_id":"you","provider_api_key":"secret"}`, cookie)
	if unknown.Code != http.StatusBadRequest || called {
		t.Fatalf("unknown status=%d called=%v", unknown.Code, called)
	}
	response := setupRequest(handler, http.MethodPost, "/api/setup/complete", body, cookie)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("complete status=%d body=%s called=%v", response.Code, response.Body.String(), called)
	}
}

func TestSetupValidationReportsOnlyNamedCredentialPresence(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	mode := testSetupMode(now, "known-token", func(config.SetupInput) error { return nil })
	mode.Validate = func(input config.SetupInput) (map[string]bool, error) {
		return map[string]bool{"EGGY_UI_PASSWORD": true, input.ProviderAPIKeyEnv: false, "EGGY_ENCRYPTION_KEY": true}, nil
	}
	handler := NewSetupModeHandler(mode)
	cookie := exchangeSetup(t, handler, "known-token")
	response := setupRequest(handler, http.MethodPost, "/api/setup/validate", `{"account_id":"you","provider_api_key_env":"MODEL_KEY"}`, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got struct {
		Variables map[string]bool `json:"variables"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Variables) != 3 || !got.Variables["EGGY_UI_PASSWORD"] || got.Variables["MODEL_KEY"] {
		t.Fatalf("variables=%#v", got.Variables)
	}
}
