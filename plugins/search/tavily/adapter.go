// Package tavily searches the web through the Tavily API. Tavily is built for
// agent use: it authenticates every request, so no datacenter IP blocking, and
// it returns extracted page content rather than a short SERP snippet.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

// maxResults is the largest page Tavily serves in one call.
const maxResults = 20

const defaultEndpoint = "https://api.tavily.com/search"

type Config struct {
	// Endpoint overrides the API URL. Blank selects the public endpoint.
	Endpoint string
	APIKey   string
	// SearchDepth is "basic" or "advanced". Blank selects "basic", which costs
	// one credit per search instead of two.
	SearchDepth string
	Timeout     time.Duration
	MaxBytes    int64
}

type Adapter struct {
	endpoint    *url.URL
	apiKey      string
	searchDepth string
	timeout     time.Duration
	maxBytes    int64
	client      *http.Client
}

type request struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	SearchDepth string `json:"search_depth"`
}

type response struct {
	Results []result `json:"results"`
	// Tavily reports quota and credential failures in this field.
	Detail json.RawMessage `json:"detail"`
}

type result struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	PublishedDate string `json:"published_date"`
}

func New(config Config, client *http.Client) (*Adapter, error) {
	endpointValue := strings.TrimSpace(config.Endpoint)
	if endpointValue == "" {
		endpointValue = defaultEndpoint
	}
	endpoint, err := url.Parse(endpointValue)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, errors.New("Tavily endpoint must be an HTTP(S) URL")
	}
	if endpoint.User != nil {
		return nil, errors.New("Tavily endpoint must not contain credentials")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("Tavily API key must not be empty")
	}
	searchDepth := strings.TrimSpace(config.SearchDepth)
	if searchDepth == "" {
		searchDepth = "basic"
	}
	if searchDepth != "basic" && searchDepth != "advanced" {
		return nil, errors.New(`Tavily search depth must be "basic" or "advanced"`)
	}
	if config.Timeout <= 0 {
		return nil, errors.New("Tavily timeout must be positive")
	}
	if config.MaxBytes <= 0 {
		return nil, errors.New("Tavily max bytes must be positive")
	}
	return &Adapter{
		endpoint:    endpoint,
		apiKey:      strings.TrimSpace(config.APIKey),
		searchDepth: searchDepth,
		timeout:     config.Timeout,
		maxBytes:    config.MaxBytes,
		client:      clientOrDefault(client),
	}, nil
}

func (a *Adapter) Search(ctx context.Context, searchRequest ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	if strings.TrimSpace(searchRequest.Query) == "" {
		return nil, errors.New("web search query must not be empty")
	}
	if searchRequest.Limit <= 0 {
		return nil, errors.New("web search result limit must be positive")
	}
	requestContext, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	body, err := json.Marshal(request{
		Query:       searchRequest.Query,
		MaxResults:  min(searchRequest.Limit, maxResults),
		SearchDepth: a.searchDepth,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Tavily search request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, a.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Tavily search request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := a.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("execute Tavily search: %w", err)
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, a.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Tavily response: %w", err)
	}
	if int64(len(responseBody)) > a.maxBytes {
		return nil, errors.New("Tavily response exceeds configured limit")
	}

	var wire response
	decodeErr := json.Unmarshal(responseBody, &wire)
	// Exhausted credits and bad keys arrive as a JSON detail body, which is far
	// more actionable than the bare status code.
	if decodeErr == nil {
		if detail := detailMessage(wire.Detail); detail != "" {
			return nil, fmt.Errorf("Tavily search failed: %s (HTTP %d)", detail, httpResponse.StatusCode)
		}
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Tavily search returned HTTP %d", httpResponse.StatusCode)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("decode Tavily response: %w", decodeErr)
	}

	results := make([]ports.WebSearchResult, 0, min(searchRequest.Limit, len(wire.Results)))
	for _, entry := range wire.Results {
		entryURL := strings.TrimSpace(entry.URL)
		if entryURL == "" {
			continue
		}
		results = append(results, ports.WebSearchResult{
			Title:       boundUTF8(strings.TrimSpace(entry.Title), 512),
			URL:         boundUTF8(entryURL, 2048),
			Snippet:     boundUTF8(strings.TrimSpace(entry.Content), 4096),
			PublishedAt: boundUTF8(strings.TrimSpace(entry.PublishedDate), 128),
		})
		if len(results) == searchRequest.Limit {
			break
		}
	}
	return results, nil
}

// detailMessage reads Tavily's error detail, which is sometimes a plain string
// and sometimes an object carrying the message.
func detailMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return boundUTF8(strings.TrimSpace(text), 512)
	}
	var object struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	if message := strings.TrimSpace(object.Error); message != "" {
		return boundUTF8(message, 512)
	}
	return boundUTF8(strings.TrimSpace(object.Message), 512)
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
