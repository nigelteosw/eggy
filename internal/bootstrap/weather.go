package bootstrap

import (
	"github.com/nigelteosw/eggy/internal/auth/connections"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/weather"
)

// newWeather builds the home page's weather service. It is always present and
// costs nothing until Home asks: no model tool, no goroutine, no prompt bytes,
// and no outbound request until a page loads it.
//
// The area and the API key are handed over as readers, not values, so what
// the settings card saves applies on the next fetch without a restart: the
// area through internal/config, the key through the sealed connection store.
func newWeather(cfg config.Config, credentials *connections.Store, options AppOptions) *weather.Service {
	bootArea := cfg.Weather.Area
	return weather.New(weather.Options{
		HTTPClient: options.HTTPClient,
		Now:        options.Now,
		Area: func() string {
			// Read at most once per cached report. A config that cannot be
			// read right now keeps the area Eggy started with.
			if current, err := config.LoadDocument(options.ConfigPath); err == nil {
				return current.Weather.Area
			}
			return bootArea
		},
		APIKey: func() string {
			if credentials == nil {
				return ""
			}
			stored, err := credentials.Read(config.WeatherConnection)
			if err != nil {
				return ""
			}
			return stored[config.WeatherAPIKeyField]
		},
	})
}
