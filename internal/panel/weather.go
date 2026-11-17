// The home page's weather: its report, and its settings card. The data.gov.sg
// API key goes into the sealed connection store, never config.yaml, and is
// read on every fetch -- so a saved key, like a changed area, applies without a
// restart.
package panel

import (
	"context"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/auth/connections"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// WeatherService is the weather service as the panel sees it. The report is
// shared: it is the same for every account, so no route asks whose it is.
type WeatherService interface {
	Report(ctx context.Context, refresh bool) (ports.WeatherReport, error)
}

func newWeatherHandler(svc WeatherService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report, err := svc.Report(r.Context(), r.URL.Query().Get("refresh") == "1")
		if err != nil {
			// The upstream error names URLs and statuses; the card only needs
			// to know the weather is unavailable right now.
			writeWebError(w, http.StatusBadGateway, "could not reach data.gov.sg; try again in a minute")
			return
		}
		writeJSON(w, report)
	}
}

type weatherSettingsView struct {
	Area  string   `json:"area"`
	Areas []string `json:"areas"`
	// KeySet says whether a key is stored; the key itself never leaves the
	// store. KeyStorable is false without EGGY_ENCRYPTION_KEY, so the card
	// can say why it cannot take one.
	KeySet      bool `json:"key_set"`
	KeyStorable bool `json:"key_storable"`
}

func weatherSettingsGetRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := weatherSettingsView{Area: cfg.Weather.Area, Areas: config.WeatherAreas, KeyStorable: webConfig.Connections != nil}
		if webConfig.Connections != nil {
			credentials, err := webConfig.Connections.Read(config.WeatherConnection)
			if err != nil {
				writeWebError(w, http.StatusInternalServerError, "could not read the stored API key")
				return
			}
			view.KeySet = credentials[config.WeatherAPIKeyField] != ""
		}
		writeJSON(w, view)
	}
}

// weatherSettingsSetRoute saves the area and, when a key was typed, seals it.
// A blank key leaves the stored one alone, so re-saving the form never erases
// it; clearing is its own route. Neither needs a restart.
func weatherSettingsSetRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Area   string `json:"area"`
			APIKey string `json:"api_key"`
		}
		if err := decodeStrictJSONBody(w, r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		key := strings.TrimSpace(input.APIKey)
		if key != "" && webConfig.Connections == nil {
			writeWebError(w, http.StatusServiceUnavailable, "the API key cannot be stored: set EGGY_ENCRYPTION_KEY and restart Eggy")
			return
		}
		// The area is checked before the key is sealed, so a refused area
		// does not leave half the form saved.
		if err := config.SetWeatherArea(configPath, input.Area); err != nil {
			writeConfigWriteError(w, err)
			return
		}
		if key != "" {
			if err := webConfig.Connections.Write(config.WeatherConnection, connections.Credentials{config.WeatherAPIKeyField: key}); err != nil {
				writeWebError(w, http.StatusInternalServerError, "could not store the API key")
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved weather."})
	}
}

func weatherKeyClearRoute(webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if webConfig.Connections == nil {
			writeWebError(w, http.StatusNotFound, "no stored API key")
			return
		}
		if err := webConfig.Connections.Write(config.WeatherConnection, connections.Credentials{config.WeatherAPIKeyField: ""}); err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not remove the API key")
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "API key removed."})
	}
}
