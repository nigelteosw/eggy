package searxng

import (
	"context"
	"errors"
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

func TestNewValidatesSearXNGConfig(t *testing.T) {
	valid := Config{BaseURL: "https://search.example.com", Timeout: time.Second, SafeSearch: 1, MaxBytes: 4096}
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{name: "missing URL", change: func(config *Config) { config.BaseURL = "" }, want: "HTTP(S) URL"},
		{name: "missing scheme", change: func(config *Config) { config.BaseURL = "search.example.com" }, want: "HTTP(S) URL"},
		{name: "unsupported scheme", change: func(config *Config) { config.BaseURL = "ftp://search.example.com" }, want: "HTTP(S) URL"},
		{name: "credentials", change: func(config *Config) { config.BaseURL = "https://token@search.example.com" }, want: "must not contain credentials"},
		{name: "query", change: func(config *Config) { config.BaseURL = "https://search.example.com?x=1" }, want: "must not contain a query or fragment"},
		{name: "fragment", change: func(config *Config) { config.BaseURL = "https://search.example.com/#x" }, want: "must not contain a query or fragment"},
		{name: "timeout", change: func(config *Config) { config.Timeout = 0 }, want: "timeout must be positive"},
		{name: "max bytes", change: func(config *Config) { config.MaxBytes = 0 }, want: "max bytes must be positive"},
		{name: "safe search", change: func(config *Config) { config.SafeSearch = 3 }, want: "safe search must be between 0 and 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.change(&config)
			_, err := New(config, http.DefaultClient)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSearchMapsSearXNGJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/private/search" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		if request.URL.Query().Get("q") != "Eggy agent" || request.URL.Query().Get("format") != "json" || request.URL.Query().Get("safesearch") != "1" {
			t.Fatalf("query=%s", request.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{
			"results": [{
				"title": "Eggy",
				"url": "https://example.com/eggy",
				"content": "A personal agent",
				"publishedDate": "2026-07-26T10:00:00Z",
				"engine": "duckduckgo",
				"engines": ["brave", "duckduckgo", "brave"]
			}]
		}`), nil
	})}
	adapter, err := New(Config{BaseURL: "https://search.example.com/private/", Timeout: time.Second, SafeSearch: 1, MaxBytes: 4096}, client)
	if err != nil {
		t.Fatal(err)
	}
	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "Eggy agent", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	want := ports.WebSearchResult{
		Title:       "Eggy",
		URL:         "https://example.com/eggy",
		Snippet:     "A personal agent",
		PublishedAt: "2026-07-26T10:00:00Z",
		Sources:     []string{"brave", "duckduckgo"},
	}
	if results[0].Title != want.Title || results[0].URL != want.URL || results[0].Snippet != want.Snippet ||
		results[0].PublishedAt != want.PublishedAt || strings.Join(results[0].Sources, ",") != strings.Join(want.Sources, ",") {
		t.Fatalf("result=%#v, want %#v", results[0], want)
	}
}

func TestSearchHonorsLimitAndSkipsBlankURLs(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[
			{"title":"skip","url":"","content":"missing URL"},
			{"title":"one","url":"https://one.example","content":"1"},
			{"title":"two","url":"https://two.example","content":"2"}
		]}`), nil
	})}
	adapter, err := New(Config{BaseURL: "http://searxng:8080", Timeout: time.Second, SafeSearch: 0, MaxBytes: 4096}, client)
	if err != nil {
		t.Fatal(err)
	}
	results, err := adapter.Search(context.Background(), ports.WebSearchRequest{Query: "x & y", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "one" {
		t.Fatalf("results=%#v", results)
	}
}

func TestSearchRejectsNonSuccessStatusWithoutLeakingBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"secret":"must-not-leak"}`), nil
	})}
	adapter, err := New(Config{BaseURL: "https://search.example.com", Timeout: time.Second, SafeSearch: 1, MaxBytes: 4096}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Search(context.Background(), ports.WebSearchRequest{Query: "news", Limit: 8})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("error=%v", err)
	}
}

func TestSearchRejectsMalformedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		maxBytes int64
		want     string
	}{
		{name: "malformed", body: `{"results":`, maxBytes: 4096, want: "decode SearXNG response"},
		{name: "oversized", body: strings.Repeat("x", 33), maxBytes: 32, want: "exceeds configured limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, test.body), nil
			})}
			adapter, err := New(Config{BaseURL: "https://search.example.com", Timeout: time.Second, SafeSearch: 1, MaxBytes: test.maxBytes}, client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Search(context.Background(), ports.WebSearchRequest{Query: "news", Limit: 8})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSearchPropagatesCancellation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	adapter, err := New(Config{BaseURL: "https://search.example.com", Timeout: time.Second, SafeSearch: 1, MaxBytes: 4096}, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.Search(ctx, ports.WebSearchRequest{Query: "news", Limit: 8})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
