package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type stubDiscovery struct {
	providers []string
	models    []ports.CatalogModel
	err       error
	asked     string
}

func (d *stubDiscovery) DiscoverableProviders() []string { return d.providers }

func (d *stubDiscovery) DiscoverModels(_ context.Context, provider string) ([]ports.CatalogModel, error) {
	d.asked = provider
	return d.models, d.err
}

func discoveryRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, query string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/config/models/available"+query, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestModelDiscoveryRouteReturnsTheProviderCatalog(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	discovery := &stubDiscovery{
		providers: []string{"openrouter"},
		models: []ports.CatalogModel{
			{ID: "anthropic/claude-sonnet-5", Name: "Claude Sonnet 5", ContextLength: 200000},
			{ID: "openai/gpt-5"},
		},
	}
	config.ModelDiscovery = discovery
	handler := NewWebHandler("", config)
	cookie := webLoginCookie(t, handler)

	response := discoveryRequest(t, handler, cookie, "?provider=openrouter")
	if response.Code != http.StatusOK || discovery.asked != "openrouter" {
		t.Fatalf("status=%d asked=%q body=%s", response.Code, discovery.asked, response.Body.String())
	}
	var result webResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"anthropic/claude-sonnet-5", "Claude Sonnet 5", "200000"},
		{"openai/gpt-5", "", ""},
	}
	if len(result.TableRows) != len(want) {
		t.Fatalf("rows=%#v", result.TableRows)
	}
	for row := range want {
		for cell := range want[row] {
			if result.TableRows[row][cell] != want[row][cell] {
				t.Fatalf("rows=%#v want=%#v", result.TableRows, want)
			}
		}
	}
	// The panel must not be able to imply that browsing enabled anything.
	if !strings.Contains(result.Detail, "add it as an alias") {
		t.Fatalf("detail=%q", result.Detail)
	}
}

func TestModelDiscoveryRouteRejectsMissingProviderAndReportsFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.ModelDiscovery = &stubDiscovery{providers: []string{"openrouter"}, err: errors.New("provider authentication failed (HTTP 401)")}
	handler := NewWebHandler("", config)
	cookie := webLoginCookie(t, handler)

	if response := discoveryRequest(t, handler, cookie, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	response := discoveryRequest(t, handler, cookie, "?provider=openrouter")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "authentication failed") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The route is owner-only for the same reason every other config route is: it
// speaks to a provider using Eggy's own credential.
func TestModelDiscoveryRouteRequiresASession(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := testWebConfig(now)
	config.ModelDiscovery = &stubDiscovery{providers: []string{"openrouter"}}
	handler := NewWebHandler("", config)

	request := httptest.NewRequest(http.MethodGet, "/api/config/models/available?provider=openrouter", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
