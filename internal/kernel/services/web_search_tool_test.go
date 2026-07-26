package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type recordingWebSearcher struct {
	request ports.WebSearchRequest
	results []ports.WebSearchResult
	err     error
}

func (s *recordingWebSearcher) Search(_ context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	s.request = request
	return s.results, s.err
}

func TestWebSearchToolDefinition(t *testing.T) {
	tool := NewWebSearchTool(&recordingWebSearcher{}, 8)
	definition := tool.Definition()
	if definition.Name != "web_search" {
		t.Fatalf("name=%q, want web_search", definition.Name)
	}
	var schema struct {
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
		Properties           struct {
			Query struct {
				MinLength int `json:"minLength"`
			} `json:"query"`
			MaxResults struct {
				Minimum int `json:"minimum"`
				Maximum int `json:"maximum"`
			} `json:"max_results"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" || schema.AdditionalProperties {
		t.Fatalf("schema contract=%s", definition.Schema)
	}
	if schema.Properties.Query.MinLength != 1 || schema.Properties.MaxResults.Minimum != 1 || schema.Properties.MaxResults.Maximum != 20 {
		t.Fatalf("schema bounds=%s", definition.Schema)
	}
}

func TestWebSearchToolUsesConfiguredDefault(t *testing.T) {
	searcher := &recordingWebSearcher{results: []ports.WebSearchResult{{
		Title: "Eggy", URL: "https://example.com/eggy", Snippet: "A personal agent",
	}}}
	tool := NewWebSearchTool(searcher, 8)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"  Eggy agent  "}`))
	if err != nil {
		t.Fatal(err)
	}
	if searcher.request != (ports.WebSearchRequest{Query: "Eggy agent", Limit: 8}) {
		t.Fatalf("request=%#v", searcher.request)
	}
	var output struct {
		Query   string                  `json:"query"`
		Results []ports.WebSearchResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.Query != "Eggy agent" || len(output.Results) != 1 || output.Results[0].Title != "Eggy" {
		t.Fatalf("output=%s", raw)
	}
}

func TestWebSearchToolAcceptsBoundedOverride(t *testing.T) {
	searcher := &recordingWebSearcher{}
	tool := NewWebSearchTool(searcher, 8)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"news","max_results":20}`)); err != nil {
		t.Fatal(err)
	}
	if searcher.request.Limit != 20 {
		t.Fatalf("limit=%d, want 20", searcher.request.Limit)
	}
}

func TestWebSearchToolRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "blank query", raw: `{"query":"   "}`, want: "query must not be empty"},
		{name: "zero override", raw: `{"query":"news","max_results":0}`, want: "max_results must be between 1 and 20"},
		{name: "negative override", raw: `{"query":"news","max_results":-1}`, want: "max_results must be between 1 and 20"},
		{name: "large override", raw: `{"query":"news","max_results":21}`, want: "max_results must be between 1 and 20"},
		{name: "unknown field", raw: `{"query":"news","engine":"google"}`, want: "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			searcher := &recordingWebSearcher{}
			_, err := NewWebSearchTool(searcher, 8).Execute(context.Background(), json.RawMessage(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want containing %q", err, test.want)
			}
			if searcher.request != (ports.WebSearchRequest{}) {
				t.Fatalf("provider called with %#v", searcher.request)
			}
		})
	}
}

func TestWebSearchToolPropagatesProviderError(t *testing.T) {
	providerErr := errors.New("search provider unavailable")
	_, err := NewWebSearchTool(&recordingWebSearcher{err: providerErr}, 8).Execute(
		context.Background(),
		json.RawMessage(`{"query":"news"}`),
	)
	if !errors.Is(err, providerErr) {
		t.Fatalf("error=%v, want provider error", err)
	}
}
