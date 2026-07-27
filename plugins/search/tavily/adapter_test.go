package tavily

import (
	"context"
	"encoding/json"
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
	adapter, err := New(Config{APIKey: "test-key", Timeout: time.Second, MaxBytes: 1 << 20},
		&http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func TestNewValidatesTavilyConfig(t *testing.T) {
	valid := Config{APIKey: "key", Timeout: time.Second, MaxBytes: 4096}
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{name: "missing API key", change: func(c *Config) { c.APIKey = " " }, want: "API key must not be empty"},
		{name: "unsupported scheme", change: func(c *Config) { c.Endpoint = "ftp://example.com" }, want: "HTTP(S) URL"},
		{name: "credentials", change: func(c *Config) { c.Endpoint = "https://token@example.com" }, want: "must not contain credentials"},
		{name: "search depth", change: func(c *Config) { c.SearchDepth = "deep" }, want: `must be "basic" or "advanced"`},
		{name: "timeout", change: func(c *Config) { c.Timeout = 0 }, want: "timeout must be positive"},
		{name: "max bytes", change: func(c *Config) { c.MaxBytes = 0 }, want: "max bytes must be positive"},
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

func TestNewDefaultsEndpointAndSearchDepth(t *testing.T) {
	adapter := newTestAdapter(t, nil)
	if got := adapter.endpoint.String(); got != defaultEndpoint {
		t.Fatalf("endpoint=%q, want %q", got, defaultEndpoint)
	}
	if adapter.searchDepth != "basic" {
		t.Fatalf("searchDepth=%q, want \"basic\"", adapter.searchDepth)
	}
}

func TestSearchMapsTavilyJSON(t *testing.T) {
	var captured *http.Request
	var body []byte
	adapter := newTestAdapter(t, func(request *http.Request) (*http.Response, error) {
		captured = request
		body, _ = io.ReadAll(request.Body)
		return jsonResponse(http.StatusOK, `{"results":[
			{"title":"First","url":"https://example.com/a","content":"extracted a","published_date":"2026-07-01"},
			{"title":"Second","url":"https://example.com/b","content":"extracted b"},
			{"title":"No URL","url":"  "}
		]}`), nil
	})

	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results)=%d, want 2 (entries without a URL are dropped)", len(results))
	}
	if results[0].Title != "First" || results[0].URL != "https://example.com/a" ||
		results[0].Snippet != "extracted a" || results[0].PublishedAt != "2026-07-01" {
		t.Fatalf("results[0]=%+v", results[0])
	}
	if results[1].PublishedAt != "" {
		t.Fatalf("results[1].PublishedAt=%q, want empty", results[1].PublishedAt)
	}

	if got := captured.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization=%q, want bearer test-key", got)
	}
	if captured.Method != http.MethodPost {
		t.Fatalf("method=%q, want POST", captured.Method)
	}
	var sent request
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent.Query != "eggy" || sent.MaxResults != 5 || sent.SearchDepth != "basic" {
		t.Fatalf("sent=%+v", sent)
	}
}

// Tavily serves at most twenty results, so a larger limit must be clamped
// rather than rejected by the API.
func TestSearchClampsLimitToTavilyMaximum(t *testing.T) {
	var sent request
	adapter := newTestAdapter(t, func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &sent)
		return jsonResponse(http.StatusOK, `{"results":[]}`), nil
	})
	if _, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 50}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if sent.MaxResults != maxResults {
		t.Fatalf("max_results=%d, want %d", sent.MaxResults, maxResults)
	}
}

func TestSearchSurfacesTavilyDetail(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "string detail", body: `{"detail":"Unauthorized: missing or invalid API key"}`},
		{name: "object detail", body: `{"detail":{"error":"Unauthorized: missing or invalid API key"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := newTestAdapter(t, func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusUnauthorized, test.body), nil
			})
			_, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "eggy", Limit: 5})
			if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
				t.Fatalf("error=%v, want the Tavily detail message", err)
			}
		})
	}
}

func TestSearchRejectsOversizedResponse(t *testing.T) {
	adapter, err := New(Config{APIKey: "key", Timeout: time.Second, MaxBytes: 16},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"results":[{"title":"a very long title","url":"https://example.com/a"}]}`), nil
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
