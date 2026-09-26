package tavily

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a client at a local server so no test reaches Tavily.
func newTestClient(t *testing.T, config Config, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config.BaseURL = server.URL
	if config.APIKey == "" {
		config.APIKey = "tvly-test"
	}
	if config.APIKeyEnv == "" {
		config.APIKeyEnv = "TAVILY_API_KEY"
	}
	return New(server.Client(), config)
}

func decodeBody(t *testing.T, request *http.Request, into any) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
}

func TestSearchSendsAuthAndConfiguredDepth(t *testing.T) {
	var seen struct {
		Query       string `json:"query"`
		SearchDepth string `json:"search_depth"`
		MaxResults  int    `json:"max_results"`
		Topic       string `json:"topic"`
		TimeRange   string `json:"time_range"`
	}
	var authorization, path string
	client := newTestClient(t, Config{SearchDepth: "advanced", MaxResults: 7}, func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		path = r.URL.Path
		decodeBody(t, r, &seen)
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	if _, err := client.Search(context.Background(), SearchRequest{Query: "  go generics  ", Topic: "news", TimeRange: "week"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if authorization != "Bearer tvly-test" {
		t.Errorf("authorization = %q", authorization)
	}
	if path != "/search" {
		t.Errorf("path = %q", path)
	}
	if seen.Query != "go generics" {
		t.Errorf("query = %q, want trimmed", seen.Query)
	}
	if seen.SearchDepth != "advanced" {
		t.Errorf("search_depth = %q", seen.SearchDepth)
	}
	// Omitted by the caller, so the configured default is what travels.
	if seen.MaxResults != 7 {
		t.Errorf("max_results = %d, want the configured default 7", seen.MaxResults)
	}
	if seen.Topic != "news" || seen.TimeRange != "week" {
		t.Errorf("topic/time_range = %q/%q", seen.Topic, seen.TimeRange)
	}
}

func TestSearchNeverAsksForAnswersOrRawContent(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		decodeBody(t, r, &body)
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	if _, err := client.Search(context.Background(), SearchRequest{Query: "q"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, field := range []string{"include_answer", "include_raw_content", "include_images"} {
		if _, present := body[field]; present {
			t.Errorf("search body must not carry %s; that is web_extract's job or the model's", field)
		}
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	client := newTestClient(t, Config{}, func(http.ResponseWriter, *http.Request) {
		t.Error("must not call the API for an empty query")
	})
	if _, err := client.Search(context.Background(), SearchRequest{Query: "   "}); err == nil {
		t.Fatal("want an error for an empty query")
	}
}

func TestSearchMapsResults(t *testing.T) {
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"query":"q","results":[{"title":"T","url":"https://e.com","content":"snippet","score":0.9}]}`)
	})
	response, err := client.Search(context.Background(), SearchRequest{Query: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d", len(response.Results))
	}
	got := response.Results[0]
	if got.Title != "T" || got.URL != "https://e.com" || got.Content != "snippet" || got.Score != 0.9 {
		t.Errorf("result = %+v", got)
	}
}

func TestExtractSendsMarkdownFormatAndURLs(t *testing.T) {
	var seen struct {
		URLs         []string `json:"urls"`
		Format       string   `json:"format"`
		ExtractDepth string   `json:"extract_depth"`
		Query        string   `json:"query"`
	}
	var path string
	client := newTestClient(t, Config{ExtractDepth: "advanced"}, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		decodeBody(t, r, &seen)
		_, _ = io.WriteString(w, `{"results":[],"failed_results":[]}`)
	})
	_, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"https://a.com", "  ", "https://b.com"}, Query: "topic"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if path != "/extract" {
		t.Errorf("path = %q", path)
	}
	if len(seen.URLs) != 2 {
		t.Errorf("urls = %v, want blanks dropped", seen.URLs)
	}
	if seen.Format != "markdown" {
		t.Errorf("format = %q", seen.Format)
	}
	if seen.ExtractDepth != "advanced" || seen.Query != "topic" {
		t.Errorf("depth/query = %q/%q", seen.ExtractDepth, seen.Query)
	}
}

func TestExtractRefusesMoreURLsThanTheCap(t *testing.T) {
	client := newTestClient(t, Config{}, func(http.ResponseWriter, *http.Request) {
		t.Error("must not spend a credit on an over-cap call")
	})
	urls := make([]string, maxExtractURLs+1)
	for i := range urls {
		urls[i] = "https://example.com"
	}
	if _, err := client.Extract(context.Background(), ExtractRequest{URLs: urls}); err == nil {
		t.Fatal("want an error above the url cap")
	}
}

func TestExtractRefusesNoURLs(t *testing.T) {
	client := newTestClient(t, Config{}, func(http.ResponseWriter, *http.Request) {
		t.Error("must not call the API with no urls")
	})
	if _, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"  "}}); err == nil {
		t.Fatal("want an error when no url survives trimming")
	}
}

func TestExtractSplitsSuccessesFromFailures(t *testing.T) {
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"url":"https://a.com","raw_content":"body"}],
			"failed_results":[{"url":"https://b.com","error":"timed out"}]}`)
	})
	response, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"https://a.com", "https://b.com"}})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].Content != "body" {
		t.Errorf("results = %+v", response.Results)
	}
	if len(response.Failed) != 1 || response.Failed[0].Error != "timed out" {
		t.Errorf("failed = %+v", response.Failed)
	}
}

// A page that could not be read is a result the model can act on, not a
// failure of the call.
func TestExtractWhereEveryURLFailsStillSucceeds(t *testing.T) {
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[],"failed_results":[{"url":"https://a.com","error":"403"}]}`)
	})
	response, err := client.Extract(context.Background(), ExtractRequest{URLs: []string{"https://a.com"}})
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if len(response.Failed) != 1 {
		t.Fatalf("failed = %+v", response.Failed)
	}
}

func TestStatusErrorsSayWhatToDo(t *testing.T) {
	for _, testCase := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "TAVILY_API_KEY"},
		{http.StatusTooManyRequests, "rate limit"},
		{432, "plan limit"},
		{433, "pay-as-you-go"},
		{http.StatusBadRequest, "rejected the request"},
		{http.StatusInternalServerError, "returned 500"},
	} {
		client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(testCase.status)
			_, _ = io.WriteString(w, "detail")
		})
		_, err := client.Search(context.Background(), SearchRequest{Query: "q"})
		if err == nil {
			t.Fatalf("status %d: want an error", testCase.status)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("status %d: error %q does not mention %q", testCase.status, err, testCase.want)
		}
	}
}

func TestErrorBodySnippetIsBounded(t *testing.T) {
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("x", 10_000))
	})
	_, err := client.Search(context.Background(), SearchRequest{Query: "q"})
	if err == nil {
		t.Fatal("want an error")
	}
	// The message adds a prefix, so the bound is on the snippet, not the whole
	// string -- allow room for it and still fail an unbounded body.
	if len(err.Error()) > maxErrorBodyBytes+200 {
		t.Errorf("error is %d bytes; a provider error page must not become a context problem", len(err.Error()))
	}
}

func TestContextCancellationPropagates(t *testing.T) {
	client := newTestClient(t, Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Search(ctx, SearchRequest{Query: "q"}); err == nil {
		t.Fatal("want an error for a cancelled context")
	}
}

func TestTruncateAtTheBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		input     string
		limit     int
		want      string
		truncated bool
	}{
		{"one under", "abcd", 5, "abcd", false},
		{"exactly at", "abcde", 5, "abcde", false},
		{"one over", "abcdef", 5, "abcde", true},
		{"zero budget", "abc", 0, "", true},
		{"zero budget empty input", "", 0, "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, truncated := truncate(testCase.input, testCase.limit)
			if got != testCase.want || truncated != testCase.truncated {
				t.Errorf("truncate(%q, %d) = %q, %v; want %q, %v",
					testCase.input, testCase.limit, got, truncated, testCase.want, testCase.truncated)
			}
		})
	}
}

// Cutting mid-rune would hand the model invalid UTF-8, which json.Marshal
// silently replaces -- a corrupted last character rather than a short one.
func TestTruncateNeverSplitsARune(t *testing.T) {
	input := strings.Repeat("é", 10) // two bytes per rune
	got, truncated := truncate(input, 5)
	if !truncated {
		t.Fatal("want truncation")
	}
	if got != "éé" {
		t.Errorf("got %q, want the last whole rune kept", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatal("truncation produced invalid UTF-8")
		}
	}
}

// One enormous page must not starve the others: the model asked for several
// answers, not one article and some empty strings.
func TestExtractBudgetIsSharedAcrossResults(t *testing.T) {
	client := newTestClient(t, Config{MaxOutputBytes: 4096}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[
			{"url":"https://a.com","raw_content":"`+strings.Repeat("a", 8000)+`"},
			{"url":"https://b.com","raw_content":"short"}],"failed_results":[]}`)
	})
	tool := extractTool{client: client}
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"urls":["https://a.com","https://b.com"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var output extractOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(output.Results) != 2 {
		t.Fatalf("results = %d", len(output.Results))
	}
	if got := len(output.Results[0].Content); got != 2048 {
		t.Errorf("long page kept %d bytes, want its 2048-byte share", got)
	}
	if !output.Results[0].Truncated {
		t.Error("long page must be marked truncated")
	}
	if output.Results[1].Content != "short" || output.Results[1].Truncated {
		t.Errorf("short page = %+v, want it untouched", output.Results[1])
	}
}

func TestSearchToolRejectsOutOfRangeAndUnknownValues(t *testing.T) {
	client := newTestClient(t, Config{}, func(http.ResponseWriter, *http.Request) {
		t.Error("must not spend a credit on invalid arguments")
	})
	tool := searchTool{client: client}
	for _, arguments := range []string{
		`{"query":"q","max_results":50}`,
		`{"query":"q","max_results":0.5}`,
		`{"query":"q","topic":"sports"}`,
		`{"query":"q","time_range":"decade"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(arguments)); err == nil {
			t.Errorf("want an error for %s", arguments)
		}
	}
}

func TestToolsAreReadOnlyAndNamed(t *testing.T) {
	tools := NewTools(New(nil, Config{}))
	if len(tools) != 2 {
		t.Fatalf("tools = %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		definition := tool.Definition()
		names[definition.Name] = true
		if !definition.Effect.ReadOnly {
			t.Errorf("%s must be ReadOnly: it changes nothing", definition.Name)
		}
		if !json.Valid(definition.Schema) {
			t.Errorf("%s has an invalid schema", definition.Name)
		}
	}
	if !names["web_search"] || !names["web_extract"] {
		t.Errorf("names = %v", names)
	}
}

// The one thing a model gets wrong here is answering from a snippet while
// believing it read the page, so the pairing has to be stated in the schema
// the model actually sees.
func TestSearchDescriptionPointsAtExtract(t *testing.T) {
	definition := searchTool{client: New(nil, Config{})}.Definition()
	if !strings.Contains(definition.Description, "web_extract") {
		t.Error("web_search must tell the model how to read a page it found")
	}
}
