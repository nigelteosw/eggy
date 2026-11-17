package config

import (
	"fmt"
	"slices"
	"strings"
)

// WeatherConfig names the one area the home page's weather is for. It is
// shared, like the deployment: the weather is the same for everyone in it.
//
// The area is read on each upstream fetch and the data.gov.sg API key lives in
// the sealed connection store (WeatherConnection), so changing either applies
// without a restart.
type WeatherConfig struct {
	Area string `yaml:"area,omitempty"`
}

// WeatherConnection is the connection ID the data.gov.sg API key is sealed
// under, and WeatherAPIKeyField its field. The key is optional upstream; it
// raises the rate limit from 6 to 30 calls per ten seconds.
const (
	WeatherConnection  = "datagovsg"
	WeatherAPIKeyField = "api_key"
)

// WeatherAreas is the one list of forecast areas, as data.gov.sg's two-hour
// forecast names them. Validation, the settings dropdown and the weather service all
// read this value. An area upstream drops still validates here; the report then
// carries no area-specific forecast rather than failing.
var WeatherAreas = []string{
	"Ang Mo Kio", "Bedok", "Bishan", "Boon Lay", "Bukit Batok", "Bukit Merah", "Bukit Panjang", "Bukit Timah",
	"Central Water Catchment", "Changi", "Choa Chu Kang", "City", "Clementi", "Geylang", "Hougang", "Jalan Bahar",
	"Jurong East", "Jurong Island", "Jurong West", "Kallang", "Lim Chu Kang", "Mandai", "Marine Parade", "Novena",
	"Pasir Ris", "Paya Lebar", "Pioneer", "Pulau Tekong", "Pulau Ubin", "Punggol", "Queenstown", "Seletar",
	"Sembawang", "Sengkang", "Sentosa", "Serangoon", "Southern Islands", "Sungei Kadut", "Tampines", "Tanglin",
	"Tengah", "Toa Payoh", "Tuas", "Western Islands", "Western Water Catchment", "Woodlands", "Yishun",
}

const defaultWeatherArea = "City"

// normalizeWeatherArea canonicalizes once, in applyDefaults: any casing the
// owner typed becomes the upstream spelling, so the weather service matches exactly.
func normalizeWeatherArea(area string) string {
	area = strings.TrimSpace(area)
	if area == "" {
		return defaultWeatherArea
	}
	for _, known := range WeatherAreas {
		if strings.EqualFold(known, area) {
			return known
		}
	}
	return area
}

func (c Config) validateWeather() error {
	if c.Weather.Area != "" && !slices.ContainsFunc(WeatherAreas, func(known string) bool { return strings.EqualFold(known, c.Weather.Area) }) {
		return fmt.Errorf("weather.area %q is not a data.gov.sg forecast area", c.Weather.Area)
	}
	return nil
}

// SetWeatherArea saves the weather area under the config lock with the same
// validation as every other write. A blank area asks for the default.
func SetWeatherArea(path string, area string) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Weather.Area = area
		return cfg.applyDefaults()
	})
}
