// Package googlecse searches the web through the Google Programmable Search
// Engine JSON API. Unlike a scraping backend it authenticates every request, so
// it is unaffected by the datacenter IP blocking that degrades SearXNG.
package googlecse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

// maxPageSize is the most results the Custom Search JSON API returns per call.
const maxPageSize = 10

const defaultEndpoint = "https://www.googleapis.com/customsearch/v1"

type Config struct {
	// Endpoint overrides the API base URL. Blank selects the public endpoint.
	Endpoint   string
	APIKey     string
	EngineID   string
	Timeout    time.Duration
	SafeSearch int
	MaxBytes   int64
}

type Adapter struct {
	endpoint   *url.URL
	apiKey     string
	engineID   string
	timeout    time.Duration
	safeSearch string
	maxBytes   int64
	client     *http.Client
}

type response struct {
	Items []item `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type item struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Snippet     string `json:"snippet"`
	DisplayLink string `json:"displayLink"`
	Pagemap     struct {
		Metatags []map[string]string `json:"metatags"`
	} `json:"pagemap"`
}

func New(config Config, client *http.Client) (*Adapter, error) {
	endpointValue := strings.TrimSpace(config.Endpoint)
	if endpointValue == "" {
		endpointValue = defaultEndpoint
	}
	endpoint, err := url.Parse(endpointValue)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, errors.New("Google CSE endpoint must be an HTTP(S) URL")
	}
	if endpoint.User != nil {
		return nil, errors.New("Google CSE endpoint must not contain credentials")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("Google CSE API key must not be empty")
	}
	if strings.TrimSpace(config.EngineID) == "" {
		return nil, errors.New("Google CSE engine ID must not be empty")
	}
	if config.Timeout <= 0 {
		return nil, errors.New("Google CSE timeout must be positive")
	}
	if config.MaxBytes <= 0 {
		return nil, errors.New("Google CSE max bytes must be positive")
	}
	if config.SafeSearch < 0 || config.SafeSearch > 2 {
		return nil, errors.New("Google CSE safe search must be between 0 and 2")
	}
	return &Adapter{
		endpoint:   endpoint,
		apiKey:     strings.TrimSpace(config.APIKey),
		engineID:   strings.TrimSpace(config.EngineID),
		timeout:    config.Timeout,
		safeSearch: safeSearchParameter(config.SafeSearch),
		maxBytes:   config.MaxBytes,
		client:     clientOrDefault(client),
	}, nil
}

func (a *Adapter) Search(ctx context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	if strings.TrimSpace(request.Query) == "" {
		return nil, errors.New("web search query must not be empty")
	}
	if request.Limit <= 0 {
		return nil, errors.New("web search result limit must be positive")
	}
	requestContext, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	// The API caps a single call at ten results, so a larger limit is served by
	// walking pages until the caller's limit or the last page is reached.
	results := make([]ports.WebSearchResult, 0, request.Limit)
	for start := 1; len(results) < request.Limit; start += maxPageSize {
		page, err := a.searchPage(requestContext, request.Query, start, min(maxPageSize, request.Limit-len(results)))
		if err != nil {
			return nil, err
		}
		results = append(results, page...)
		if len(page) < maxPageSize {
			break
		}
	}
	if len(results) > request.Limit {
		results = results[:request.Limit]
	}
	return results, nil
}

func (a *Adapter) searchPage(ctx context.Context, query string, start, count int) ([]ports.WebSearchResult, error) {
	searchURL := *a.endpoint
	values := searchURL.Query()
	values.Set("key", a.apiKey)
	values.Set("cx", a.engineID)
	values.Set("q", query)
	values.Set("num", strconv.Itoa(count))
	values.Set("start", strconv.Itoa(start))
	values.Set("safe", a.safeSearch)
	searchURL.RawQuery = values.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Google CSE search request: %w", err)
	}
	httpResponse, err := a.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("execute Google CSE search: %w", err)
	}
	defer httpResponse.Body.Close()

	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, a.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Google CSE response: %w", err)
	}
	if int64(len(body)) > a.maxBytes {
		return nil, errors.New("Google CSE response exceeds configured limit")
	}

	var wire response
	decodeErr := json.Unmarshal(body, &wire)
	// Google reports quota exhaustion and bad credentials in a JSON error body,
	// which is far more actionable than the bare status code.
	if decodeErr == nil && wire.Error != nil {
		return nil, fmt.Errorf("Google CSE search failed: %s (HTTP %d)", wire.Error.Message, wire.Error.Code)
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Google CSE search returned HTTP %d", httpResponse.StatusCode)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("decode Google CSE response: %w", decodeErr)
	}

	results := make([]ports.WebSearchResult, 0, len(wire.Items))
	for _, entry := range wire.Items {
		link := strings.TrimSpace(entry.Link)
		if link == "" {
			continue
		}
		result := ports.WebSearchResult{
			Title:       boundUTF8(strings.TrimSpace(entry.Title), 512),
			URL:         boundUTF8(link, 2048),
			Snippet:     boundUTF8(strings.TrimSpace(entry.Snippet), 4096),
			PublishedAt: boundUTF8(publishedAt(entry), 128),
		}
		if source := boundUTF8(strings.TrimSpace(entry.DisplayLink), 128); source != "" {
			result.Sources = []string{source}
		}
		results = append(results, result)
	}
	return results, nil
}

// publishedAt recovers a publication date from the page metadata Google echoes
// back. The API has no dedicated field, and most pages carry none of these.
func publishedAt(entry item) string {
	keys := []string{"article:published_time", "datepublished", "og:updated_time", "date"}
	for _, metatags := range entry.Pagemap.Metatags {
		for _, key := range keys {
			if value := strings.TrimSpace(metatags[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func safeSearchParameter(safeSearch int) string {
	// The API exposes only off/active, so SearXNG's moderate tier maps to active
	// to keep the stricter reading when a config is shared between adapters.
	if safeSearch == 0 {
		return "off"
	}
	return "active"
}

func clientOrDefault(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

func boundUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
