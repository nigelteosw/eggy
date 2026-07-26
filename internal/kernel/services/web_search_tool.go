package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

type webSearchTool struct {
	searcher       ports.WebSearcher
	defaultResults int
}

type webSearchInput struct {
	Query      string `json:"query"`
	MaxResults *int   `json:"max_results,omitempty"`
}

type webSearchOutput struct {
	Query   string                  `json:"query"`
	Results []ports.WebSearchResult `json:"results"`
}

func NewWebSearchTool(searcher ports.WebSearcher, defaultResults int) ports.Tool {
	if defaultResults < 1 || defaultResults > 20 {
		defaultResults = 8
	}
	return webSearchTool{searcher: searcher, defaultResults: defaultResults}
}

func (t webSearchTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "web_search",
		Description: "Search the current web and return bounded source results",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1},"max_results":{"type":"integer","minimum":1,"maximum":20}},"required":["query"],"additionalProperties":false}`),
	}
}

func (t webSearchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input webSearchInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, errors.New("query must not be empty")
	}
	limit := t.defaultResults
	if input.MaxResults != nil {
		limit = *input.MaxResults
	}
	if limit < 1 || limit > 20 {
		return nil, errors.New("max_results must be between 1 and 20")
	}
	results, err := t.searcher.Search(ctx, ports.WebSearchRequest{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []ports.WebSearchResult{}
	}
	return json.Marshal(webSearchOutput{Query: query, Results: results})
}
