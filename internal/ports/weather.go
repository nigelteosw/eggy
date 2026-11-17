package ports

import "time"

// WeatherReport is the home page's answer to "what is it like out", for the
// one configured area. It is the panel's view and the weather service's
// output, so it lives here: the panel never imports the provider.
//
// A section the upstream could not supply is left nil rather than failing the
// whole report -- a missing humidity reading should not blank the forecast.
type WeatherReport struct {
	// Area is the forecast area the report is for; Region is the broad part
	// of the country its period forecasts are read from.
	Area   string `json:"area"`
	Region string `json:"region,omitempty"`
	// FetchedAt is when Eggy last asked upstream. Stale is set when the last
	// attempt failed and this is the previous good answer.
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"stale,omitempty"`

	Now     *WeatherNow      `json:"now,omitempty"`
	Today   *WeatherDay      `json:"today,omitempty"`
	Periods []WeatherPeriod  `json:"periods,omitempty"`
	Outlook []WeatherOutlook `json:"outlook,omitempty"`
}

// WeatherNow is the latest station readings nearest the area, and the
// short-range forecast for the area itself.
type WeatherNow struct {
	// Temperature and Humidity are nil when no station reported.
	Temperature *float64  `json:"temperature,omitempty"`
	Humidity    *float64  `json:"humidity,omitempty"`
	Station     string    `json:"station,omitempty"`
	ObservedAt  time.Time `json:"observed_at,omitempty"`
	// Forecast is the next-two-hours condition, e.g. "Thundery Showers", and
	// ValidText the window it covers as upstream words it ("1 pm to 3 pm").
	Forecast  string `json:"forecast,omitempty"`
	ValidText string `json:"valid_text,omitempty"`
}

// WeatherDay is the national 24-hour outlook: the day's range and its headline.
type WeatherDay struct {
	Forecast      string `json:"forecast"`
	Low           int    `json:"low"`
	High          int    `json:"high"`
	HumidityLow   int    `json:"humidity_low"`
	HumidityHigh  int    `json:"humidity_high"`
	WindLow       int    `json:"wind_low"`
	WindHigh      int    `json:"wind_high"`
	WindDirection string `json:"wind_direction,omitempty"`
}

// WeatherPeriod is one block of the 24-hour forecast for the area's region.
type WeatherPeriod struct {
	Label    string    `json:"label"`
	Text     string    `json:"text"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Forecast string    `json:"forecast"`
}

// WeatherOutlook is one day of the four-day outlook.
type WeatherOutlook struct {
	Day      string `json:"day"`
	Date     string `json:"date"`
	Forecast string `json:"forecast"`
	Summary  string `json:"summary,omitempty"`
	Low      int    `json:"low"`
	High     int    `json:"high"`
}
