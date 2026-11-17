package panel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/connections"
	"github.com/nigelteosw/eggy/internal/auth/grants"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeWeather struct {
	report  ports.WeatherReport
	err     error
	refresh bool
}

func (f *fakeWeather) Report(_ context.Context, refresh bool) (ports.WeatherReport, error) {
	f.refresh = refresh
	return f.report, f.err
}

func weatherHandler(t *testing.T, weather WeatherService) (http.Handler, *http.Cookie) {
	t.Helper()
	webConfig := testWebConfig(t, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if weather != nil {
		webConfig.Weather = weather
	}
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

func TestWeatherReportRoute(t *testing.T) {
	temperature := 28.4
	fake := &fakeWeather{report: ports.WeatherReport{Area: "Bedok", Now: &ports.WeatherNow{Temperature: &temperature}}}
	handler, cookie := weatherHandler(t, fake)
	response := call(t, handler, cookie, http.MethodGet, "/api/weather?refresh=1", "")
	var report ports.WeatherReport
	decode(t, response, &report)
	if response.Code != http.StatusOK || report.Area != "Bedok" || *report.Now.Temperature != 28.4 || !fake.refresh {
		t.Fatalf("status=%d report=%+v refresh=%v", response.Code, report, fake.refresh)
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/weather", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("without a session = %d, want 401", unauthenticated.Code)
	}
}

func TestWeatherUpstreamFailureDoesNotLeakDetail(t *testing.T) {
	handler, cookie := weatherHandler(t, &fakeWeather{err: errors.New("GET https://api-open.data.gov.sg/v2: 429")})
	response := call(t, handler, cookie, http.MethodGet, "/api/weather", "")
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "api-open") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// The settings card saves the area to config and the API key sealed, never to
// config.yaml, and neither asks for a restart.
func TestWeatherSettingsSealTheKeyAndApplyWithoutRestart(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	cfg, database, _ := accountWebConfig(t, now)
	sealer, err := grants.NewSealer("connections", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	credentials := connections.New(database.Auth(), sealer)
	cfg.Connections = credentials
	cfg.Weather = &fakeWeather{}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountConfigYAML()), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewWebHandler(path, cfg)
	cookie, csrf := signIn(t, handler, database, "nigel", now)
	do := func(method, target, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		attachSession(request, cookie)
		request.Header.Set(csrfHeader, csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	view := func() weatherSettingsView {
		response := do(http.MethodGet, "/api/config/weather", "")
		var view weatherSettingsView
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return view
	}
	if v := view(); v.Area != "City" || v.KeySet || !v.KeyStorable || len(v.Areas) != len(config.WeatherAreas) {
		t.Fatalf("initial view = %+v", v)
	}

	if response := do(http.MethodPost, "/api/config/weather", `{"area":"Atlantis","api_key":"k1"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown area: status=%d", response.Code)
	}
	if stored, _ := credentials.Read(config.WeatherConnection); stored[config.WeatherAPIKeyField] != "" {
		t.Fatal("a refused save still sealed the key")
	}

	response := do(http.MethodPost, "/api/config/weather", `{"area":"bedok","api_key":" data-key "}`)
	var result webResult
	decode(t, response, &result)
	if response.Code != http.StatusOK || result.Detail != "" {
		t.Fatalf("save: status=%d result=%+v (nothing here waits on a restart)", response.Code, result)
	}
	if v := view(); v.Area != "Bedok" || !v.KeySet {
		t.Fatalf("view after save = %+v", v)
	}
	if stored, _ := credentials.Read(config.WeatherConnection); stored[config.WeatherAPIKeyField] != "data-key" {
		t.Fatalf("stored = %v", stored)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "data-key") {
		t.Fatal("the key reached config.yaml")
	}
	if strings.Contains(do(http.MethodGet, "/api/config/weather", "").Body.String(), "data-key") {
		t.Fatal("the key is echoed back to the panel")
	}

	// A blank key keeps the stored one.
	if response := do(http.MethodPost, "/api/config/weather", `{"area":"Tampines","api_key":""}`); response.Code != http.StatusOK || !view().KeySet || view().Area != "Tampines" {
		t.Fatalf("re-save: status=%d view=%+v", response.Code, view())
	}
	if response := do(http.MethodDelete, "/api/config/weather/key", ""); response.Code != http.StatusOK || view().KeySet {
		t.Fatalf("clear: status=%d keySet=%v", response.Code, view().KeySet)
	}
}
