package googlecse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func newTestAdapter(t *testing.T, transport roundTripFunc) *Adapter {
	t.Helper()
	adapter, err := New(Config{
		APIKey: "test-key", EngineID: "test-cx", Timeout: time.Second, SafeSearch: 1, MaxBytes: 1 << 20,
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func TestNewValidatesGoogleCSEConfig(t *testing.T) {
	valid := Config{APIKey: "key", EngineID: "cx", Timeout: time.Second, SafeSearch: 1, MaxBytes: 4096}
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{name: "missing API key", change: func(c *Config) { c.APIKey = " " }, want: "API key must not be empty"},
		{name: "missing engine ID", change: func(c *Config) { c.EngineID = "" }, want: "engine ID must not be empty"},
		{name: "unsupported scheme", change: func(c *Config) { c.Endpoint = "ftp://example.com" }, want: "HTTP(S) URL"},
		{name: "credentials", change: func(c *Config) { c.Endpoint = "https://token@example.com" }, want: "must not contain credentials"},
		{name: "timeout", change: func(c *Config) { c.Timeout = 0 }, want: "timeout must be positive"},
		{name: "max bytes", change: func(c *Config) { c.MaxBytes = 0 }, want: "max bytes must be positive"},
		{name: "safe search", change: func(c *Config) { c.SafeSearch = 3 }, want: "safe search must be between 0 and 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.change(&config)
			if _, err := New(config, http.DefaultClient); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewDefaultsToPublicEndpoint(t *testing.T) {
	adapter := newTestAdapter(t, nil)
	if got := adapter.endpoint.String(); got != defaultEndpoint {
		t.Fatalf("endpoint=%q, want %q", got, defaultEndpoint)
	}
}

func TestSearchMapsGoogleCSEJSON(t *testing.T) {
	var captured *http.Request
	adapter := newTestAdapter(t, func(request *http.Request) (*http.Response, error) {
		captured = request
		return jsonResponse(http.StatusOK, `{"items":[
			{"title":"First","link":"https://example.com/a","snippet":"snippet a","displayLink":"example.com",
			 "pagemap":{"metatags":[{"article:published_time":"2026-07-01"}]}},
			{"title":"Second","link":"https://example.com/b","snippet":"snippet b"},
			{"title":"No link","link":"  "}
		]}`), nil
	})

	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results)=%d, want 2 (entries without a URL are dropped)", len(results))
	}
	want := ports.WebSearchResult{
		Title: "First", URL: "https://example.com/a", Snippet: "snippet a",
		PublishedAt: "2026-07-01", Sources: []string{"example.com"},
	}
	if results[0].Title != want.Title || results[0].URL != want.URL || results[0].Snippet != want.Snippet ||
		results[0].PublishedAt != want.PublishedAt || len(results[0].Sources) != 1 || results[0].Sources[0] != "example.com" {
		t.Fatalf("results[0]=%+v, want %+v", results[0], want)
	}
	if results[1].PublishedAt != "" || results[1].Sources != nil {
		t.Fatalf("results[1]=%+v, want empty published date and sources", results[1])
	}

	query := captured.URL.Query()
	for name, want := range map[string]string{"key": "test-key", "cx": "test-cx", "q": "eggy", "safe": "active", "start": "1", "num": "5"} {
		if got := query.Get(name); got != want {
			t.Fatalf("query %s=%q, want %q", name, got, want)
		}
	}
}

func TestSearchPagesBeyondTenResults(t *testing.T) {
	var starts []string
	adapter := newTestAdapter(t, func(request *http.Request) (*http.Response, error) {
		starts = append(starts, request.URL.Query().Get("start"))
		items := make([]string, 0, maxPageSize)
		for i := range maxPageSize {
			items = append(items, fmt.Sprintf(`{"title":"t","link":"https://example.com/%s-%d"}`, request.URL.Query().Get("start"), i))
		}
		return jsonResponse(http.StatusOK, `{"items":[`+strings.Join(items, ",")+`]}`), nil
	})

	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 15})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 15 {
		t.Fatalf("len(results)=%d, want 15", len(results))
	}
	if len(starts) != 2 || starts[0] != "1" || starts[1] != "11" {
		t.Fatalf("starts=%v, want [1 11]", starts)
	}
}

func TestSearchStopsOnShortPage(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, func(*http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusOK, `{"items":[{"title":"t","link":"https://example.com/a"}]}`), nil
	})

	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 15})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || calls != 1 {
		t.Fatalf("len(results)=%d calls=%d, want 1 and 1", len(results), calls)
	}
}

func TestSearchSurfacesGoogleErrorBody(t *testing.T) {
	adapter := newTestAdapter(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests,
			`{"error":{"code":429,"message":"Quota exceeded for quota metric 'Queries'"}}`), nil
	})

	_, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 5})
	if err == nil || !strings.Contains(err.Error(), "Quota exceeded") {
		t.Fatalf("error=%v, want the Google quota message", err)
	}
}

func TestSearchRejectsOversizedResponse(t *testing.T) {
	adapter, err := New(Config{
		APIKey: "key", EngineID: "cx", Timeout: time.Second, SafeSearch: 1, MaxBytes: 16,
	}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"items":[{"title":"a very long title","link":"https://example.com/a"}]}`), nil
	})})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 5}); err == nil ||
		!strings.Contains(err.Error(), "exceeds configured limit") {
		t.Fatalf("error=%v, want the size limit error", err)
	}
}

func TestSearchValidatesRequest(t *testing.T) {
	adapter := newTestAdapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request for an invalid search request")
		return nil, nil
	})
	if _, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "  ", Limit: 5}); err == nil {
		t.Fatal("blank query: error=nil, want an error")
	}
	if _, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 0}); err == nil {
		t.Fatal("zero limit: error=nil, want an error")
	}
}

func TestSafeSearchParameterMapsToGoogleValues(t *testing.T) {
	for safeSearch, want := range map[int]string{0: "off", 1: "active", 2: "active"} {
		if got := safeSearchParameter(safeSearch); got != want {
			t.Fatalf("safeSearchParameter(%d)=%q, want %q", safeSearch, got, want)
		}
	}
}
