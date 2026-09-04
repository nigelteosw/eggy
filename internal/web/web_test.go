package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/plugins/auth/session"
	"github.com/nigelteosw/eggy/plugins/channels/webchat"
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
	// The sixth attempt is refused outright rather than slept on, so a
	// throttled attacker cannot pin a server goroutine per request.
	start := time.Now()
	response := badLogin()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("throttled attempt blocked the handler for %v", elapsed)
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if retryAfter := response.Header().Get("Retry-After"); retryAfter != "2" {
		t.Fatalf("Retry-After=%q", retryAfter)
	}
}

// Behind a proxy every request carries the proxy's RemoteAddr, so keying the
// throttle on it alone puts every attempt in one bucket: guessing is barely
// slowed and an attacker locks the owner out. TrustedProxyHops states how many
// proxies Eggy sits behind so the real client can be read off X-Forwarded-For.
func TestWebLoginThrottleKeysPerClientBehindTrustedProxy(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.TrustedProxyHops = 1
	handler := NewWebHandler("", config)
	badLogin := func(forwardedFor string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`))
		request.RemoteAddr = "10.0.0.1:443"
		request.Header.Set("X-Forwarded-For", forwardedFor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for i := 0; i < 6; i++ {
		badLogin("9.9.9.9")
	}
	if code := badLogin("9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Fatalf("attacker not throttled: status=%d", code)
	}
	if code := badLogin("8.8.8.8").Code; code != http.StatusUnauthorized {
		t.Fatalf("a different client shares the attacker's bucket: status=%d", code)
	}
}

// A spoofed X-Forwarded-For must not let an attacker mint a fresh bucket per
// attempt, so the header is ignored entirely when no proxy is configured.
func TestWebLoginThrottleIgnoresForwardedForWithoutTrustedProxy(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	badLogin := func(forwardedFor string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`))
		request.RemoteAddr = "9.9.9.9:12345"
		request.Header.Set("X-Forwarded-For", forwardedFor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for i := 0; i < 6; i++ {
		badLogin("1.2.3." + strconv.Itoa(i))
	}
	if code := badLogin("5.5.5.5").Code; code != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For evaded the throttle: status=%d", code)
	}
}

func TestClientIPSelectsHopFromTheRight(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		hops         int
		remoteAddr   string
		forwardedFor string
		want         string
	}{
		{"no proxy uses remote addr", 0, "9.9.9.9:1", "1.1.1.1", "9.9.9.9"},
		{"one hop takes the last entry", 1, "10.0.0.1:1", "1.1.1.1, 2.2.2.2", "2.2.2.2"},
		{"two hops skip the inner proxy", 2, "10.0.0.1:1", "1.1.1.1, 2.2.2.2, 3.3.3.3", "2.2.2.2"},
		{"short chain falls back to remote addr", 2, "10.0.0.1:1", "1.1.1.1", "10.0.0.1"},
		{"missing header falls back to remote addr", 1, "10.0.0.1:1", "", "10.0.0.1"},
		{"unparseable entry falls back to remote addr", 1, "10.0.0.1:1", "not-an-ip", "10.0.0.1"},
		{"ipv6 entry is preserved", 1, "10.0.0.1:1", "2001:db8::1", "2001:db8::1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/login", nil)
			request.RemoteAddr = testCase.remoteAddr
			if testCase.forwardedFor != "" {
				request.Header.Set("X-Forwarded-For", testCase.forwardedFor)
			}
			if got := clientIP(request, testCase.hops); got != testCase.want {
				t.Fatalf("clientIP()=%q want %q", got, testCase.want)
			}
		})
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
	var decoded webResult
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

func TestWebModelRouteRemovesANonDefaultAlias(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	if err := config.SetModelAlias(path, "deepseek-fast", "deepseek", "deepseek-v4-flash", ""); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, testWebConfig(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodDelete, "/api/config/models/deepseek-fast", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	loaded, err := config.LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := loaded.ModelAliases["deepseek-fast"]; exists {
		t.Fatal("deleted alias remains in config")
	}
}

func TestWebConfigRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", testWebConfig(now))
	for _, path := range []string{"/api/config/providers", "/api/config/models", "/api/config/mcp", "/api/config/google", "/api/config/heartbeat"} {
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
	var listed webResult
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
	var afterRemove webResult
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

func TestWebHandlerMountsChatRoutes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.ChatHub = webchat.NewHub()
	memory := newTestMemoryStore(t)
	config.Memory, config.Threads = memory, memory
	enqueued := false
	config.Enqueue = func(context.Context, events.Event) error {
		enqueued = true
		return nil
	}
	config.OwnerID = "owner-42"
	handler := NewWebHandler("", config)
	cookie := webLoginCookie(t, handler)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/chat/threads", nil)
	createRequest.AddCookie(cookie)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/chat/threads/"+created.ID+"/send", strings.NewReader(`{"text":"hi"}`))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || !enqueued {
		t.Fatalf("status=%d enqueued=%v", response.Code, enqueued)
	}
}

func TestWebHandlerDoesNotExposeTheRemovedFileAPI(t *testing.T) {
	handler := NewWebHandler("", WebUIConfig{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/files/MEMORY.md", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("removed file API status=%d, want 404", response.Code)
	}
}

func TestWebHandlerChatRoutesRequireSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.ChatHub = webchat.NewHub()
	config.Memory = newTestMemoryStore(t)
	config.Enqueue = func(context.Context, events.Event) error { return nil }
	handler := NewWebHandler("", config)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/chat/threads", nil),
		httptest.NewRequest(http.MethodPost, "/api/chat/threads", nil),
		httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-1/stream", nil),
		httptest.NewRequest(http.MethodPost, "/api/chat/threads/thread-1/send", strings.NewReader(`{"text":"hi"}`)),
		httptest.NewRequest(http.MethodPost, "/api/chat/approve", strings.NewReader(`{"approval_id":"x","approved":true}`)),
		httptest.NewRequest(http.MethodGet, "/api/chat/threads/thread-1/history", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", request.URL.Path, response.Code)
		}
	}
}

type fakeMCPLogin struct {
	url      string
	err      error
	requests []string
}

func (f *fakeMCPLogin) BeginLogin(_ context.Context, server string) (string, error) {
	f.requests = append(f.requests, server)
	return f.url, f.err
}

// TestWebMCPLoginRedirectsAndRequiresSession covers the route that makes the
// OAuth callback reachable at all, and the reason it must be owner-gated: an
// anonymous visitor who could start a flow would bind their own account as
// Eggy's credential for that server.
func TestWebMCPLoginRedirectsAndRequiresSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runtime := &fakeMCPLogin{url: "https://accounts.google.com/o/oauth2/v2/auth?state=abc"}
	webConfig := testWebConfig(now)
	webConfig.MCP = runtime
	handler := NewWebHandler(writeConfigFile(t, validConfig()), webConfig)

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/auth/mcp/calendar", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", anonymous.Code)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("an anonymous request started an OAuth flow: %v", runtime.requests)
	}

	request := httptest.NewRequest(http.MethodGet, "/auth/mcp/calendar", nil)
	request.AddCookie(webLoginCookie(t, handler))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != runtime.url {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if len(runtime.requests) != 1 || runtime.requests[0] != "calendar" {
		t.Fatalf("requests=%v", runtime.requests)
	}
}

// Without a manager there is no login route at all, rather than one that
// panics on a nil runtime.
func TestWebMCPLoginIsAbsentWithoutAManager(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(writeConfigFile(t, validConfig()), testWebConfig(now))
	request := httptest.NewRequest(http.MethodGet, "/auth/mcp/calendar", nil)
	request.AddCookie(webLoginCookie(t, handler))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusFound {
		t.Fatalf("a login redirect was served with no MCP manager: %d", response.Code)
	}
}

// The Google section is the one an owner otherwise had to reach by editing
// /data/config.yaml by hand. It round-trips through the same section routes
// providers and models use.
func TestWebGoogleSectionRoundTrips(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setBody := strings.NewReader(`{"enabled":"true","client_id":"x.apps.googleusercontent.com","client_secret_env":"GOOGLE_CLIENT_SECRET","products":"calendar,gmail"}`)
	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/google", setBody)
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/google", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	var decoded webResult
	if err := json.Unmarshal(getResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	// One row, because there is one grant. A second would imply per-product
	// configuration that does not exist.
	if len(decoded.TableRows) != 1 {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
	row := decoded.TableRows[0]
	if row[0] != "enabled" || row[1] != "x.apps.googleusercontent.com" || row[3] != "calendar, gmail" {
		t.Fatalf("row=%#v", row)
	}
}

// The approval list is the one Google setting where getting it wrong is a
// safety problem rather than an inconvenience, and it has three states the
// panel has to be able to reach and read back.
func TestWebGoogleApprovalsRoundTripAllThreeStates(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	webConfig := testWebConfig(now)
	webConfig.GoogleActions = map[string]GoogleProductActions{
		"gmail":    {Actions: []string{"search", "get", "send", "reply"}, Mutations: []string{"send", "reply"}},
		"calendar": {Actions: []string{"list", "create", "delete"}, Mutations: []string{"create", "delete"}},
	}
	handler := NewWebHandler(path, webConfig)
	cookie := webLoginCookie(t, handler)

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/config/google", strings.NewReader(body))
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	read := func() map[string]string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/config/google", nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var decoded webResult
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		fields := map[string]string{}
		for _, field := range decoded.Fields {
			fields[field.Label] = field.Value
		}
		return fields
	}

	const client = `"enabled":"true","client_id":"x.apps.googleusercontent.com","products":"calendar,gmail"`
	if code := post(`{` + client + `,"require_approval_mode":"custom","require_approval":"gmail.send,calendar.delete"}`).Code; code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	fields := read()
	if fields["require_approval_mode"] != "custom" || fields["require_approval"] != "gmail.send,calendar.delete" {
		t.Fatalf("fields=%v", fields)
	}
	// The catalog travels too, because the form builds its checkboxes from it
	// rather than from a copy of the action list.
	if fields["actions.gmail"] != "search,get,send,reply" || fields["mutations.gmail"] != "send,reply" {
		t.Fatalf("catalog not served: %v", fields)
	}

	// Custom with nothing named is the owner saying nothing should ask, and it
	// must not read back as the default.
	if code := post(`{` + client + `,"require_approval_mode":"custom","require_approval":""}`).Code; code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fields := read(); fields["require_approval_mode"] != "custom" || fields["require_approval"] != "" {
		t.Fatalf("an empty custom list read back as %v", fields)
	}

	// Back to the default, which stores no list at all so a later version's
	// new actions are gated without another edit.
	if code := post(`{` + client + `,"require_approval_mode":"default"}`).Code; code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fields := read(); fields["require_approval_mode"] != "default" {
		t.Fatalf("fields=%v", fields)
	}
}

// An action the adapter would reject at startup has to be refused here, while
// the owner still has a working config: a bad require_approval entry fails the
// next boot, and a config edit that lands them in safe mode is a poor way to
// learn about a typo.
func TestWebGoogleApprovalsRejectUnknownActions(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	webConfig := testWebConfig(now)
	webConfig.GoogleActions = map[string]GoogleProductActions{
		"gmail": {Actions: []string{"search", "send"}, Mutations: []string{"send"}},
	}
	handler := NewWebHandler(path, webConfig)
	cookie := webLoginCookie(t, handler)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range []string{"gmail.snd", "gmail", "telegram.send"} {
		body := `{"enabled":"true","client_id":"x.apps.googleusercontent.com","products":"gmail","require_approval_mode":"custom","require_approval":"` + entry + `"}`
		request := httptest.NewRequest(http.MethodPost, "/api/config/google", strings.NewReader(body))
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%q: status=%d body=%s", entry, response.Code, response.Body.String())
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused approval list still rewrote the config")
	}
}

// The same validation the chat command and the config loader apply: a rejected
// write leaves the file the owner had.
func TestWebGoogleSectionRejectsAnUnknownProduct(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	body := strings.NewReader(`{"enabled":"true","client_id":"x.apps.googleusercontent.com","products":"gmial"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/config/google", body)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The heartbeat panel writes config.yaml like every other section, so the
// response says so: bootstrap reads the interval once at startup, and a form
// that looked live would be a lie on the one setting an owner reaches for
// when the heartbeat is bothering them.
func TestWebHeartbeatSectionRoundTripsAndSaysRestartIsNeeded(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/heartbeat", strings.NewReader(`{"interval":"3h","instruction":"watch the deploy"}`))
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}
	var saved webResult
	if err := json.Unmarshal(setResponse.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.Detail, "Restart") {
		t.Fatalf("detail=%q, want the restart-to-apply notice", saved.Detail)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/heartbeat", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	var decoded webResult
	if err := json.Unmarshal(getResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 1 || decoded.TableRows[0][0] != "3h0m0s" || decoded.TableRows[0][1] != "watch the deploy" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}

// Off is reported as "off", not as "0s": off is the state the owner is
// looking for, and a duration that means off reads like a misconfiguration.
func TestWebHeartbeatSectionReportsOffWhenUnset(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/config/heartbeat", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var decoded webResult
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.TableRows) != 1 || decoded.TableRows[0][0] != "off" {
		t.Fatalf("rows=%#v", decoded.TableRows)
	}
}

func TestWebHeartbeatSectionRejectsAnUnparseableInterval(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/config/heartbeat", strings.NewReader(`{"interval":"every 3 hours"}`))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// Appearance round-trips like every other section, and the theme it stores is
// what the pre-auth probe then reports.
func TestWebAppearanceSectionRoundTrips(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))
	cookie := webLoginCookie(t, handler)

	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/appearance", strings.NewReader(`{"theme":"light"}`))
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/appearance", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	var decoded webResult
	if err := json.Unmarshal(getResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Fields) != 1 || decoded.Fields[0].Value != "light" {
		t.Fatalf("fields=%#v", decoded.Fields)
	}
}

// The probe carries the theme so the panel paints in it the first time rather
// than flipping after the session check. It is unauthenticated by design --
// the login page has to honour the theme too.
func TestModeProbeCarriesTheConfiguredTheme(t *testing.T) {
	path := writeConfigFile(t, validConfig()+"appearance:\n  theme: light\n")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mode", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Mode  string `json:"mode"`
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode != "normal" || decoded.Theme != "light" {
		t.Fatalf("decoded=%#v", decoded)
	}
}

// A config it cannot read must not fail the probe: the mode is the
// load-bearing half, and blanking the panel over a cosmetic preference would
// hide the one screen that could fix it.
func TestModeProbeFallsBackToDarkWhenConfigIsUnreadable(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(filepath.Join(t.TempDir(), "absent.yaml"), testWebConfig(now))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mode", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"dark"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The raw editor began as safe mode's repair screen, which meant config.yaml
// was only editable from a browser once Eggy had already failed to start. The
// running panel serves the same two routes.
func TestWebRawConfigRoundTripsInNormalMode(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	webConfig := testWebConfig(now)
	webConfig.Getenv = testGetenv
	handler := NewWebHandler(path, webConfig)
	cookie := webLoginCookie(t, handler)

	getRequest := httptest.NewRequest(http.MethodGet, "/api/config/raw", nil)
	getRequest.AddCookie(cookie)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	if !strings.Contains(getResponse.Body.String(), "default_model") {
		t.Fatalf("body did not look like config.yaml: %s", getResponse.Body.String())
	}

	setRequest := httptest.NewRequest(http.MethodPost, "/api/config/raw", strings.NewReader(getResponse.Body.String()))
	setRequest.AddCookie(cookie)
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("set status=%d body=%s", setResponse.Code, setResponse.Body.String())
	}
}

// A config the running Eggy would not load is refused, and the file it already
// had survives -- the same guarantee safe mode makes.
func TestWebRawConfigRejectionLeavesTheFileAlone(t *testing.T) {
	original := validConfig()
	path := writeConfigFile(t, original)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	webConfig := testWebConfig(now)
	webConfig.Getenv = testGetenv
	handler := NewWebHandler(path, webConfig)
	cookie := webLoginCookie(t, handler)

	request := httptest.NewRequest(http.MethodPost, "/api/config/raw", strings.NewReader("agent: [not a mapping]\n"))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatal("a refused config was written anyway")
	}
}

// The editor is owner-only: config.yaml names every provider, repository, and
// secret variable the deployment uses.
func TestWebRawConfigRequiresASession(t *testing.T) {
	path := writeConfigFile(t, validConfig())
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewWebHandler(path, testWebConfig(now))

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/config/raw", nil),
		httptest.NewRequest(http.MethodPost, "/api/config/raw", strings.NewReader("agent: {}\n")),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", request.Method, response.Code)
		}
	}
}

// testGetenv answers the variables validConfig names and nothing else. A stub
// that answered everything would also answer PORT, which has to parse as one.
func testGetenv(name string) string {
	return map[string]string{
		"DEEPSEEK_API_KEY":        "key",
		"TELEGRAM_BOT_TOKEN":      "token",
		"TELEGRAM_WEBHOOK_SECRET": "secret",
		"GITHUB_TOKEN":            "gh",
	}[name]
}

// The /web link is the panel's second door, so it is worth checking it opens
// exactly once and only for a token this deployment signed.
func TestLoginLinkSignsInOnceAndOnlyOnce(t *testing.T) {
	key := []byte("signing-key")
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", WebUIConfig{SigningKey: key, Now: func() time.Time { return now }})
	token := session.SignLoginLink(key, now.Add(webLoginLinkTTL))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/link?token="+url.QueryEscape(token), nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	cookie := response.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != webSessionCookie || !session.VerifySession(key, cookie[0].Value, now) {
		t.Fatalf("cookies=%v", cookie)
	}

	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, httptest.NewRequest(http.MethodGet, "/auth/link?token="+url.QueryEscape(token), nil))
	if replay.Code != http.StatusUnauthorized || len(replay.Result().Cookies()) != 0 {
		t.Fatalf("replayed link status=%d cookies=%v", replay.Code, replay.Result().Cookies())
	}

	forged := httptest.NewRecorder()
	forgedToken := session.SignLoginLink([]byte("another-key"), now.Add(webLoginLinkTTL))
	handler.ServeHTTP(forged, httptest.NewRequest(http.MethodGet, "/auth/link?token="+url.QueryEscape(forgedToken), nil))
	if forged.Code != http.StatusUnauthorized || len(forged.Result().Cookies()) != 0 {
		t.Fatalf("forged link status=%d cookies=%v", forged.Code, forged.Result().Cookies())
	}
}

func TestLoginLinkOpensAnAuthenticatedSession(t *testing.T) {
	key := []byte("signing-key")
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	handler := NewWebHandler("", WebUIConfig{SigningKey: key, Now: func() time.Time { return now }})
	response := httptest.NewRecorder()
	token := session.SignLoginLink(key, now.Add(webLoginLinkTTL))
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/link?token="+url.QueryEscape(token), nil))

	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	probe := httptest.NewRecorder()
	handler.ServeHTTP(probe, request)
	if probe.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", probe.Code, probe.Body.String())
	}
}
