package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
