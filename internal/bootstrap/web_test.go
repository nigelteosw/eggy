package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testWebConfig(now time.Time) WebUIConfig {
	return WebUIConfig{
		UserEmail: "owner@example.com", Password: "hunter2",
		SigningKey: []byte("test-signing-key"),
		Now:        func() time.Time { return now },
	}
}

func TestWebLoginSucceedsAndSetsSessionCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	body := strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "eggy_session" || cookies[0].Value == "" {
		t.Fatalf("cookies=%#v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie=%#v", cookies[0])
	}
}

func TestWebLoginRejectsWrongPassword(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	body := strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("expected no cookie on failed login")
	}
}

func TestWebLoginRejectsWhenNotConfigured(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.UserEmail, config.Password = "", ""
	handler := NewWebHandler("", config)

	body := strings.NewReader(`{"email":"anyone@example.com","password":"anything"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/login", body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestWebSessionRequiresValidCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	unauthed := httptest.NewRecorder()
	handler.ServeHTTP(unauthed, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if unauthed.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", unauthed.Code)
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	cookie := login.Result().Cookies()[0]

	authed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(authed, request)
	if authed.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", authed.Code, authed.Body.String())
	}
}

func TestWebLogoutClearsSessionCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/logout", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("expected a clearing cookie (negative MaxAge), got %#v", cookies)
	}
}

func TestWebLoginThrottlesRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	badLogin := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`))
		request.RemoteAddr = "9.9.9.9:12345"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for i := 0; i < 5; i++ {
		if badLogin().Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401", i)
		}
	}
	start := time.Now()
	badLogin()
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("expected the 6th attempt to be delayed ~2s, took %v", elapsed)
	}
}

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func webLoginCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestWebConfigRoutesRoundTripThroughCommandService(t *testing.T) {
	// validConfig() and config_test.go are in this same package (bootstrap),
	// so it's reused directly here rather than duplicating its YAML.
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setBody := strings.NewReader(`{"name":"deepseek","adapter":"openai_compatible","base_url":"https://api.deepseek.com","api_key_env":"DEEPSEEK_API_KEY"}`)
	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/providers", setBody)
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/providers", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	var decoded CommandResult
	if err := json.Unmarshal(getResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range decoded.TableRows {
		if len(row) > 0 && row[0] == "deepseek" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the newly set provider in table rows: %#v", decoded.TableRows)
	}
}

func TestWebConfigRoutesRejectInvalidInputLikeCLIAndTelegram(t *testing.T) {
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/providers", strings.NewReader(`{"name":"deepseek"}`))
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}
}

func TestWebConfigRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	for _, path := range []string{"/api/config/providers", "/api/config/models", "/api/config/calendar"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}

func TestWebMCPRoutesAddEditRemoveRoundTrip(t *testing.T) {
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	addBody := strings.NewReader(`{"name":"railway","url":"https://mcp.railway.com","auth":"oauth","enabled":true}`)
	addRequest := httptest.NewRequest(http.MethodPost, "/api/config/mcp", addBody)
	addRequest.AddCookie(cookie)
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", addResponse.Code, addResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/config/mcp", nil)
	listRequest.AddCookie(cookie)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var listed CommandResult
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range listed.TableRows {
		if len(row) > 0 && row[0] == "railway" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected railway in table rows: %#v", listed.TableRows)
	}

	removeRequest := httptest.NewRequest(http.MethodDelete, "/api/config/mcp/railway", nil)
	removeRequest.AddCookie(cookie)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, removeRequest)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", removeResponse.Code, removeResponse.Body.String())
	}

	afterRemoveRequest := httptest.NewRequest(http.MethodGet, "/api/config/mcp", nil)
	afterRemoveRequest.AddCookie(cookie)
	afterRemoveResponse := httptest.NewRecorder()
	handler.ServeHTTP(afterRemoveResponse, afterRemoveRequest)
	var afterRemove CommandResult
	if err := json.Unmarshal(afterRemoveResponse.Body.Bytes(), &afterRemove); err != nil {
		t.Fatal(err)
	}
	if len(afterRemove.TableRows) != 0 {
		t.Fatalf("expected no servers after removal, got %#v", afterRemove.TableRows)
	}
}

func TestWebMCPRoutesRejectInvalidInput(t *testing.T) {
	path := writeConfigFile(t, validConfig())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	addRequest := httptest.NewRequest(http.MethodPost, "/api/config/mcp", strings.NewReader(`{"name":"railway","url":"http://mcp.railway.com","auth":"oauth","enabled":true}`))
	addRequest.AddCookie(cookie)
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", addResponse.Code, addResponse.Body.String())
	}

	removeRequest := httptest.NewRequest(http.MethodDelete, "/api/config/mcp/does-not-exist", nil)
	removeRequest.AddCookie(cookie)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, removeRequest)
	if removeResponse.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", removeResponse.Code, removeResponse.Body.String())
	}
}

func TestWebMCPRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/api/config/mcp", nil))
	if getResponse.Code != http.StatusUnauthorized {
		t.Fatalf("get status=%d", getResponse.Code)
	}

	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/config/mcp/railway", nil))
	if deleteResponse.Code != http.StatusUnauthorized {
		t.Fatalf("delete status=%d", deleteResponse.Code)
	}
}

func TestWebResponseBodyIsRenderJSONShape(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"hunter2"}`)))
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["state"] != "success" {
		t.Fatalf("decoded=%#v", decoded)
	}
}
