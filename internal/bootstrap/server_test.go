package bootstrap

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPHandlerHealthAndReadiness(t *testing.T) {
	readyErr := errors.New("calendar unavailable")
	telegramCalls := 0
	handler := NewHTTPHandler(func() error { return readyErr }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telegramCalls++
		w.WriteHeader(http.StatusNoContent)
	}), nil, nil)

	for _, tc := range []struct {
		path string
		want int
	}{{"/healthz", 200}, {"/readyz", 503}, {"/missing", 404}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.want {
			t.Fatalf("%s status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
	}
	readyErr = nil
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/webhooks/telegram", nil))
	if response.Code != http.StatusNoContent || telegramCalls != 1 {
		t.Fatalf("telegram status=%d calls=%d", response.Code, telegramCalls)
	}
}

func TestHTTPHandlerOptionalGoogleRoutes(t *testing.T) {
	start := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTemporaryRedirect) })
	callback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := NewHTTPHandler(func() error { return nil }, nil, start, callback)
	for _, tc := range []struct {
		path string
		want int
	}{{"/auth/google", 307}, {"/auth/google/callback", 204}, {"/webhooks/telegram", 503}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.want {
			t.Fatalf("%s status=%d", tc.path, response.Code)
		}
	}
}

func TestHTTPHandlerOptionalMCPCallbackRoute(t *testing.T) {
	callback := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.PathValue("server") != "railway" {
			t.Fatalf("server=%q", request.PathValue("server"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := NewHTTPHandler(func() error { return nil }, nil, nil, nil, callback)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/mcp/railway/callback?code=x&state=y", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d", response.Code)
	}
}
