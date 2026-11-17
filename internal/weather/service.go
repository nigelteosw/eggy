// Package weather is the home page's live Singapore weather, from data.gov.sg,
// its one provider. One report is shared by every account -- the weather is
// the same for everyone in the deployment -- and cached briefly, so a
// household opening Eggy at once costs upstream one round of calls.
//
// It costs nothing until Home asks: no model tool, no prompt bytes, no
// background loop. A fetch happens only when a page loads it.
package weather

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	// freshFor is how long a report is served without asking again. Station
	// readings arrive each minute and the two-hour forecast every half hour,
	// so two minutes keeps the page live without spending the rate limit.
	freshFor = 2 * time.Minute
	// refreshFloor is the youngest report an explicit refresh replaces, so
	// a held-down refresh button cannot exhaust the keyless limit of six
	// calls per ten seconds (one report is five).
	refreshFloor = 30 * time.Second
	fetchTimeout = 10 * time.Second
)

// Options wires the service. APIKey and Area are read on every fetch, which is
// what lets a key or area saved in settings apply without a restart.
type Options struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     func() string
	Area       func() string
	Now        func() time.Time
}

// Service is the one place a weather report is built. The panel's home card is
// a thin caller of Report.
type Service struct {
	client *client
	area   func() string
	now    func() time.Time

	mu     sync.Mutex
	cached *ports.WeatherReport
}

func New(options Options) *Service {
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL := options.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	area := options.Area
	if area == nil {
		area = func() string { return "City" }
	}
	return &Service{client: &client{http: httpClient, baseURL: baseURL, apiKey: options.APIKey}, area: area, now: now}
}

// Report answers the current weather for the configured area. refresh asks for
// a new fetch even when the cached one is still fresh, down to refreshFloor.
//
// The lock is held across the fetch on purpose: callers arriving together wait
// for one upstream round rather than each starting their own.
func (s *Service) Report(ctx context.Context, refresh bool) (ports.WeatherReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	area := s.area()
	now := s.now()
	if s.cached != nil && s.cached.Area == area {
		age := now.Sub(s.cached.FetchedAt)
		if age < refreshFloor || (!refresh && age < freshFor) {
			return *s.cached, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	report, err := s.fetch(ctx, area, now)
	if err != nil {
		// Upstream down or rate-limited: the last good report, marked, beats
		// an empty card. It is never re-stamped, so it stays visibly old.
		if s.cached != nil && s.cached.Area == area {
			stale := *s.cached
			stale.Stale = true
			return stale, nil
		}
		return ports.WeatherReport{}, err
	}
	s.cached = &report
	return report, nil
}

// fetch asks the five endpoints at once and builds what it can. Only when every
// one failed is the report an error.
func (s *Service) fetch(ctx context.Context, area string, now time.Time) (ports.WeatherReport, error) {
	var (
		wg                         sync.WaitGroup
		twoHour                    twoHourData
		day                        dayData
		outlook                    outlookData
		temperature, humidity      readingsData
		errTwo, errDay, errOutlook error
		errTemp, errHumidity       error
	)
	wg.Add(5)
	go func() { defer wg.Done(); twoHour, errTwo = get[twoHourData](ctx, s.client, "two-hr-forecast") }()
	go func() { defer wg.Done(); day, errDay = get[dayData](ctx, s.client, "twenty-four-hr-forecast") }()
	go func() { defer wg.Done(); outlook, errOutlook = get[outlookData](ctx, s.client, "four-day-outlook") }()
	go func() { defer wg.Done(); temperature, errTemp = get[readingsData](ctx, s.client, "air-temperature") }()
	go func() { defer wg.Done(); humidity, errHumidity = get[readingsData](ctx, s.client, "relative-humidity") }()
	wg.Wait()
	if errTwo != nil && errDay != nil && errOutlook != nil && errTemp != nil && errHumidity != nil {
		return ports.WeatherReport{}, errors.Join(errTwo, errDay, errOutlook, errTemp, errHumidity)
	}

	report := ports.WeatherReport{Area: area, FetchedAt: now}
	// Where the area is. An area upstream no longer lists falls back to the
	// city centre, which still gives a sensible nearest station.
	here := location{Latitude: 1.2929, Longitude: 103.8547}
	if errTwo == nil {
		for _, meta := range twoHour.AreaMetadata {
			if meta.Name == area {
				here = meta.LabelLocation
			}
		}
	}
	report.Region = regionOf(here)

	nowView := &ports.WeatherNow{}
	if errTwo == nil && len(twoHour.Items) > 0 {
		item := twoHour.Items[0]
		nowView.ValidText = item.ValidPeriod.Text
		for _, forecast := range item.Forecasts {
			if forecast.Area == area {
				nowView.Forecast = forecast.Forecast
			}
		}
	}
	if errTemp == nil {
		if value, station, at, ok := nearestReading(temperature, here); ok {
			nowView.Temperature, nowView.Station, nowView.ObservedAt = &value, station, at
		}
	}
	if errHumidity == nil {
		if value, _, _, ok := nearestReading(humidity, here); ok {
			nowView.Humidity = &value
		}
	}
	if nowView.Forecast != "" || nowView.Temperature != nil || nowView.Humidity != nil {
		report.Now = nowView
	}

	if errDay == nil && len(day.Records) > 0 {
		record := day.Records[0]
		general := record.General
		report.Today = &ports.WeatherDay{
			Forecast: general.Forecast.Text,
			Low:      whole(general.Temperature.Low), High: whole(general.Temperature.High),
			HumidityLow: whole(general.RelativeHumidity.Low), HumidityHigh: whole(general.RelativeHumidity.High),
			WindLow: whole(general.Wind.Speed.Low), WindHigh: whole(general.Wind.Speed.High),
			WindDirection: general.Wind.Direction,
		}
		for _, period := range record.Periods {
			forecast, ok := period.Regions[report.Region]
			if !ok {
				continue
			}
			report.Periods = append(report.Periods, ports.WeatherPeriod{
				Label: periodLabel(period.TimePeriod.Start, period.TimePeriod.End, now), Text: period.TimePeriod.Text,
				Start: period.TimePeriod.Start, End: period.TimePeriod.End, Forecast: forecast.Text,
			})
		}
	}

	if errOutlook == nil && len(outlook.Records) > 0 {
		for _, forecast := range outlook.Records[0].Forecasts {
			report.Outlook = append(report.Outlook, ports.WeatherOutlook{
				Day: forecast.Day, Date: forecast.Timestamp.Format(time.DateOnly),
				Forecast: forecast.Forecast.Text, Summary: forecast.Forecast.Summary,
				Low: whole(forecast.Temperature.Low), High: whole(forecast.Temperature.High),
			})
		}
	}
	return report, nil
}

// nearestReading is the latest reading from the reporting station closest to
// here. Stations come and go from a reading set, so only those in it count.
func nearestReading(data readingsData, here location) (float64, string, time.Time, bool) {
	if len(data.Readings) == 0 {
		return 0, "", time.Time{}, false
	}
	latest := data.Readings[0]
	values := make(map[string]float64, len(latest.Data))
	for _, reading := range latest.Data {
		values[reading.StationID] = reading.Value
	}
	best, bestName, bestValue, found := math.Inf(1), "", 0.0, false
	for _, station := range data.Stations {
		value, ok := values[station.ID]
		if !ok {
			continue
		}
		if d := distance(here, station.Location); d < best {
			best, bestName, bestValue, found = d, station.Name, value, true
		}
	}
	return bestValue, bestName, latest.Timestamp, found
}

// regions are rough centres of the five regions the 24-hour forecast speaks of.
// The country is small enough that the nearest centre is the right region.
var regions = map[string]location{
	"central": {1.3521, 103.8198},
	"north":   {1.4304, 103.8200},
	"south":   {1.2800, 103.8300},
	"east":    {1.3500, 103.9400},
	"west":    {1.3500, 103.7000},
}

func regionOf(here location) string {
	best, name := math.Inf(1), "central"
	for _, region := range []string{"central", "north", "south", "east", "west"} {
		if d := distance(here, regions[region]); d < best {
			best, name = d, region
		}
	}
	return name
}

// distance is squared degrees, which orders points correctly at this scale.
func distance(a, b location) float64 {
	dLat, dLon := a.Latitude-b.Latitude, a.Longitude-b.Longitude
	return dLat*dLat + dLon*dLon
}

func whole(value float64) int { return int(math.Round(value)) }

// periodLabel names a forecast block the way a person would say it, against
// the day it is now in the block's own (Singapore) offset. The block already
// under way is "Now", whichever day it began.
func periodLabel(start, end, now time.Time) string {
	if !now.Before(start) && now.Before(end) {
		return "Now"
	}
	local := now.In(start.Location())
	part := "night"
	switch hour := start.Hour(); {
	case hour < 6:
		part = "early morning"
	case hour < 12:
		part = "morning"
	case hour < 18:
		part = "afternoon"
	}
	sameDay := start.Year() == local.Year() && start.YearDay() == local.YearDay()
	switch {
	case sameDay && part == "night":
		return "Tonight"
	case sameDay:
		return "This " + part
	case start.After(local):
		return "Tomorrow " + part
	}
	return "Earlier " + part
}
