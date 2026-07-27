package searxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

type Config struct {
	BaseURL    string
	Timeout    time.Duration
	SafeSearch int
	MaxBytes   int64
}

type Adapter struct {
	baseURL    *url.URL
	timeout    time.Duration
	safeSearch int
	maxBytes   int64
	client     *http.Client
}

type response struct {
	Results []result `json:"results"`
}

type result struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Content       string   `json:"content"`
	PublishedDate string   `json:"publishedDate"`
	Engine        string   `json:"engine"`
	Engines       []string `json:"engines"`
}

func New(config Config, client *http.Client) (*Adapter, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, errors.New("SearXNG base URL must be an HTTP(S) URL")
	}
	if baseURL.User != nil {
		return nil, errors.New("SearXNG base URL must not contain credentials")
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("SearXNG base URL must not contain a query or fragment")
	}
	if config.Timeout <= 0 {
		return nil, errors.New("SearXNG timeout must be positive")
	}
	if config.MaxBytes <= 0 {
		return nil, errors.New("SearXNG max bytes must be positive")
	}
	if config.SafeSearch < 0 || config.SafeSearch > 2 {
		return nil, errors.New("SearXNG safe search must be between 0 and 2")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copyURL := *baseURL
	copyURL.Path = strings.TrimRight(copyURL.Path, "/")
	return &Adapter{
		baseURL:    &copyURL,
		timeout:    config.Timeout,
		safeSearch: config.SafeSearch,
		maxBytes:   config.MaxBytes,
		client:     client,
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

	searchURL := *a.baseURL
	searchURL.Path = strings.TrimRight(searchURL.Path, "/") + "/search"
	values := searchURL.Query()
	values.Set("q", request.Query)
	values.Set("format", "json")
	values.Set("safesearch", strconv.Itoa(a.safeSearch))
	searchURL.RawQuery = values.Encode()

	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create SearXNG search request: %w", err)
	}
	httpResponse, err := a.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("execute SearXNG search: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("SearXNG search returned HTTP %d", httpResponse.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, a.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read SearXNG response: %w", err)
	}
	if int64(len(body)) > a.maxBytes {
		return nil, errors.New("SearXNG response exceeds configured limit")
	}
	var wire response
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decode SearXNG response: %w", err)
	}

	results := make([]ports.WebSearchResult, 0, min(request.Limit, len(wire.Results)))
	for _, item := range wire.Results {
		itemURL := strings.TrimSpace(item.URL)
		if itemURL == "" {
			continue
		}
		results = append(results, ports.WebSearchResult{
			Title:       boundUTF8(strings.TrimSpace(item.Title), 512),
			URL:         boundUTF8(itemURL, 2048),
			Snippet:     boundUTF8(strings.TrimSpace(item.Content), 4096),
			PublishedAt: boundUTF8(strings.TrimSpace(item.PublishedDate), 128),
			Sources:     normalizeSources(item.Engine, item.Engines),
		})
		if len(results) == request.Limit {
			break
		}
	}
	return results, nil
}

func normalizeSources(engine string, engines []string) []string {
	seen := map[string]bool{}
	for _, source := range append(append([]string(nil), engines...), engine) {
		source = boundUTF8(strings.TrimSpace(source), 128)
		if source != "" {
			seen[source] = true
		}
	}
	sources := make([]string, 0, min(len(seen), 16))
	for source := range seen {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	if len(sources) > 16 {
		sources = sources[:16]
	}
	return sources
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
