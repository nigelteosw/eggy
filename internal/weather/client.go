package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultBaseURL is data.gov.sg's real-time API. It is a field rather than a
// constant only so tests can point the client at an httptest server.
const defaultBaseURL = "https://api-open.data.gov.sg/v2/real-time/api"

// maxBody caps one upstream answer. The largest (rainfall's seventy stations)
// is a few tens of kilobytes; anything near this is not a weather report.
const maxBody = 1 << 20

// client speaks to data.gov.sg. Its wire types stay in this file.
type client struct {
	http    *http.Client
	baseURL string
	apiKey  func() string
}

// envelope is every real-time answer: code 0 and data, or an error message.
type envelope[T any] struct {
	Code     int    `json:"code"`
	Data     T      `json:"data"`
	ErrorMsg string `json:"errorMsg"`
}

func get[T any](ctx context.Context, c *client, path string) (T, error) {
	var zero T
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+path, nil)
	if err != nil {
		return zero, err
	}
	request.Header.Set("Accept", "application/json")
	// The key is read per call, so one saved in settings applies to the next
	// fetch without a restart. Without one the API still answers, at a lower
	// rate limit.
	if c.apiKey != nil {
		if key := c.apiKey(); key != "" {
			request.Header.Set("x-api-key", key)
		}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The body is not repeated: an upstream error page is not the owner's
		// to read, and the status says what kind of failure it was.
		return zero, fmt.Errorf("%s: data.gov.sg answered %s", path, response.Status)
	}
	var body envelope[T]
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(&body); err != nil {
		return zero, fmt.Errorf("%s: decode: %w", path, err)
	}
	if body.Code != 0 {
		return zero, fmt.Errorf("%s: data.gov.sg error %d: %s", path, body.Code, body.ErrorMsg)
	}
	return body.Data, nil
}

type location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type twoHourData struct {
	AreaMetadata []struct {
		Name          string   `json:"name"`
		LabelLocation location `json:"label_location"`
	} `json:"area_metadata"`
	Items []struct {
		ValidPeriod struct {
			Text string `json:"text"`
		} `json:"valid_period"`
		Forecasts []struct {
			Area     string `json:"area"`
			Forecast string `json:"forecast"`
		} `json:"forecasts"`
	} `json:"items"`
}

type forecastText struct {
	Code    string `json:"code"`
	Text    string `json:"text"`
	Summary string `json:"summary"`
}

type rangeOf struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type wind struct {
	Direction string  `json:"direction"`
	Speed     rangeOf `json:"speed"`
}

type dayData struct {
	Records []struct {
		General struct {
			Forecast         forecastText `json:"forecast"`
			RelativeHumidity rangeOf      `json:"relativeHumidity"`
			Temperature      rangeOf      `json:"temperature"`
			Wind             wind         `json:"wind"`
		} `json:"general"`
		Periods []struct {
			Regions    map[string]forecastText `json:"regions"`
			TimePeriod struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
				Text  string    `json:"text"`
			} `json:"timePeriod"`
		} `json:"periods"`
	} `json:"records"`
}

type outlookData struct {
	Records []struct {
		Forecasts []struct {
			Day         string       `json:"day"`
			Forecast    forecastText `json:"forecast"`
			Temperature rangeOf      `json:"temperature"`
			Timestamp   time.Time    `json:"timestamp"`
		} `json:"forecasts"`
	} `json:"records"`
}

// readingsData is the station-reading shape air-temperature and
// relative-humidity share.
type readingsData struct {
	Stations []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Location location `json:"location"`
	} `json:"stations"`
	Readings []struct {
		Timestamp time.Time `json:"timestamp"`
		Data      []struct {
			StationID string  `json:"stationId"`
			Value     float64 `json:"value"`
		} `json:"data"`
	} `json:"readings"`
}
