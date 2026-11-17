package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Abbreviated real answers, recorded from api-open.data.gov.sg on 2026-11-17.
var fixtures = map[string]string{
	"two-hr-forecast": `{"code":0,"errorMsg":"","data":{
		"area_metadata":[{"name":"Bedok","label_location":{"latitude":1.321,"longitude":103.924}},
		                 {"name":"Jurong West","label_location":{"latitude":1.34,"longitude":103.705}}],
		"items":[{"valid_period":{"text":"10.30 pm to 12.30 am"},
		  "forecasts":[{"area":"Bedok","forecast":"Thundery Showers"},{"area":"Jurong West","forecast":"Cloudy"}]}]}}`,
	"twenty-four-hr-forecast": `{"code":0,"errorMsg":"","data":{"records":[{
		"general":{"forecast":{"code":"TL","text":"Thundery Showers"},
		  "relativeHumidity":{"high":95,"low":60,"unit":"Percentage"},
		  "temperature":{"high":33,"low":24,"unit":"Degrees Celsius"},
		  "wind":{"direction":"VARIABLE","speed":{"high":15,"low":5}}},
		"periods":[
		  {"regions":{"central":{"code":"CL","text":"Cloudy"},"east":{"code":"TL","text":"Thundery Showers"},"west":{"code":"CL","text":"Cloudy"}},
		   "timePeriod":{"start":"2026-11-17T18:00:00+08:00","end":"2026-11-18T06:00:00+08:00","text":"6 pm 17 Nov to 6 am 18 Nov"}},
		  {"regions":{"central":{"code":"PC","text":"Partly Cloudy (Day)"},"east":{"code":"PC","text":"Partly Cloudy (Day)"},"west":{"code":"PC","text":"Partly Cloudy (Day)"}},
		   "timePeriod":{"start":"2026-11-18T06:00:00+08:00","end":"2026-11-18T12:00:00+08:00","text":"6 am to Midday 18 Nov"}}]}]}}`,
	"four-day-outlook": `{"code":0,"errorMsg":"","data":{"records":[{"forecasts":[
		{"day":"Wednesday","forecast":{"code":"TL","summary":"Afternoon thundery showers","text":"Thundery Showers"},
		 "temperature":{"high":33.4,"low":24,"unit":"Degrees Celsius"},"timestamp":"2026-11-18T00:00:00+08:00"}]}]}}`,
	"air-temperature": `{"code":0,"errorMsg":"","data":{
		"stations":[{"id":"S107","name":"East Coast Parkway","location":{"latitude":1.3135,"longitude":103.9625}},
		            {"id":"S44","name":"Nanyang Avenue","location":{"latitude":1.3458,"longitude":103.6817}},
		            {"id":"S99","name":"Silent","location":{"latitude":1.321,"longitude":103.924}}],
		"readings":[{"timestamp":"2026-11-17T22:53:00+08:00","data":[{"stationId":"S107","value":27.7},{"stationId":"S44","value":25.1}]}]}}`,
	"relative-humidity": `{"code":0,"errorMsg":"","data":{
		"stations":[{"id":"S107","name":"East Coast Parkway","location":{"latitude":1.3135,"longitude":103.9625}}],
		"readings":[{"timestamp":"2026-11-17T22:53:00+08:00","data":[{"stationId":"S107","value":88.5}]}]}}`,
}

var singapore = time.FixedZone("SGT", 8*3600)

type upstream struct {
	server *httptest.Server
	calls  atomic.Int64
	keys   atomic.Value
	fail   atomic.Bool
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	u.keys.Store("")
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.calls.Add(1)
		u.keys.Store(r.Header.Get("x-api-key"))
		if u.fail.Load() {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		body, ok := fixtures[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(u.server.Close)
	return u
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time         { return c.at }
func (c *clock) add(step time.Duration) { c.at = c.at.Add(step) }

func newService(u *upstream, area *string, key *string, c *clock) *Service {
	return New(Options{
		HTTPClient: u.server.Client(), BaseURL: u.server.URL,
		Area: func() string { return *area }, APIKey: func() string { return *key }, Now: c.now,
	})
}

func TestReportBuildsTheAreasWeather(t *testing.T) {
	u := newUpstream(t)
	area, key := "Bedok", ""
	c := &clock{at: time.Date(2026, 11, 17, 22, 55, 0, 0, singapore)}
	report, err := newService(u, &area, &key, c).Report(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Area != "Bedok" || report.Region != "east" {
		t.Fatalf("area/region = %q/%q, want Bedok/east", report.Area, report.Region)
	}
	now := report.Now
	if now == nil || now.Forecast != "Thundery Showers" || now.ValidText != "10.30 pm to 12.30 am" {
		t.Fatalf("now = %+v", now)
	}
	// The silent station is nearest Bedok but reported nothing, so the
	// nearest one that did is used.
	if now.Temperature == nil || *now.Temperature != 27.7 || now.Station != "East Coast Parkway" {
		t.Fatalf("temperature = %v from %q", now.Temperature, now.Station)
	}
	if now.Humidity == nil || *now.Humidity != 88.5 {
		t.Fatalf("humidity = %v", now.Humidity)
	}
	if today := report.Today; today == nil || today.Low != 24 || today.High != 33 || today.WindHigh != 15 || today.Forecast != "Thundery Showers" {
		t.Fatalf("today = %+v", report.Today)
	}
	if len(report.Periods) != 2 || report.Periods[0].Label != "Now" || report.Periods[0].Forecast != "Thundery Showers" || report.Periods[1].Label != "Tomorrow morning" {
		t.Fatalf("periods = %+v", report.Periods)
	}
	if len(report.Outlook) != 1 || report.Outlook[0].High != 33 || report.Outlook[0].Date != "2026-11-18" || report.Outlook[0].Summary == "" {
		t.Fatalf("outlook = %+v", report.Outlook)
	}
}

func TestReportIsSharedAndCachedButFollowsSettings(t *testing.T) {
	u := newUpstream(t)
	area, key := "Bedok", ""
	c := &clock{at: time.Date(2026, 11, 17, 14, 0, 0, 0, singapore)}
	service := newService(u, &area, &key, c)
	ctx := context.Background()

	if _, err := service.Report(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := u.calls.Load(); got != 5 {
		t.Fatalf("first report made %d calls, want 5", got)
	}
	if u.keys.Load() != "" {
		t.Fatal("sent a key when none is set")
	}
	// A refresh inside the floor is still the cached report.
	c.add(10 * time.Second)
	if _, _ = service.Report(ctx, true); u.calls.Load() != 5 {
		t.Fatal("a refresh inside the floor reached upstream")
	}
	c.add(time.Minute)
	if _, _ = service.Report(ctx, false); u.calls.Load() != 5 {
		t.Fatal("a fresh report was fetched again")
	}
	// A key saved in settings applies on the next fetch, with no restart.
	key = "secret-key"
	c.add(2 * time.Minute)
	if _, _ = service.Report(ctx, false); u.calls.Load() != 10 || u.keys.Load() != "secret-key" {
		t.Fatalf("calls=%d key=%v after the cache aged out", u.calls.Load(), u.keys.Load())
	}
	// So does an area change, which a cached report for another area cannot answer.
	area = "Jurong West"
	report, err := service.Report(ctx, false)
	if err != nil || report.Area != "Jurong West" || report.Region != "west" || u.calls.Load() != 15 {
		t.Fatalf("area change: %+v, calls=%d, err=%v", report, u.calls.Load(), err)
	}
	if report.Now.Station != "Nanyang Avenue" {
		t.Fatalf("station = %q, want the one nearest Jurong West", report.Now.Station)
	}
}

func TestReportServesTheLastGoodOneWhenUpstreamFails(t *testing.T) {
	u := newUpstream(t)
	area, key := "Bedok", ""
	c := &clock{at: time.Date(2026, 11, 17, 14, 0, 0, 0, singapore)}
	service := newService(u, &area, &key, c)

	u.fail.Store(true)
	if _, err := service.Report(context.Background(), false); err == nil {
		t.Fatal("a report with every endpoint failing was not an error")
	} else if strings.Contains(err.Error(), "slow down") {
		t.Fatal("the upstream body leaked into the error")
	}

	u.fail.Store(false)
	fresh, err := service.Report(context.Background(), false)
	if err != nil || fresh.Stale {
		t.Fatalf("fresh report: stale=%v err=%v", fresh.Stale, err)
	}
	u.fail.Store(true)
	c.add(5 * time.Minute)
	stale, err := service.Report(context.Background(), false)
	if err != nil || !stale.Stale || !stale.FetchedAt.Equal(fresh.FetchedAt) {
		t.Fatalf("stale report: %+v err=%v", stale, err)
	}
}

func TestPeriodLabels(t *testing.T) {
	at := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, singapore) }
	now := at(17, 9)
	for _, tc := range []struct {
		start, end time.Time
		want       string
	}{
		{at(17, 6), at(17, 12), "Now"},
		{at(17, 12), at(17, 18), "This afternoon"},
		{at(17, 18), at(18, 6), "Tonight"},
		{at(18, 6), at(18, 12), "Tomorrow morning"},
		{at(18, 0), at(18, 6), "Tomorrow early morning"},
	} {
		if got := periodLabel(tc.start, tc.end, now); got != tc.want {
			t.Errorf("periodLabel(%v) = %q, want %q", tc.start, got, tc.want)
		}
	}
}
