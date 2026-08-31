package tavily

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nigelteosw/eggy/internal/ports"
)

// NewTools returns the two tools this capability adds.
//
// Both are ReadOnly: searching and reading public pages change nothing, so
// ModeNormal does not put them to the owner. ModeStrict still does, like every
// other tool.
func NewTools(client *Client) []ports.Tool {
	return []ports.Tool{
		searchTool{client: client},
		extractTool{client: client},
	}
}

var (
	knownTopics     = []string{"general", "news", "finance"}
	knownTimeRanges = []string{"day", "week", "month", "year"}
)

type searchTool struct{ client *Client }

func (t searchTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:   "web_search",
		Effect: ports.ReadOnlyTool(),
		// The description says what the results are not, because the failure
		// this tool actually has is a model answering from a snippet while
		// believing it read the page.
		Description: "Search the live web. Returns ranked results with title, url and a short snippet of the page -- not the page itself. To read an actual page, follow up with web_extract on the urls this returns. topic=news favours recent reporting and topic=finance favours market sources; time_range limits results by publish date.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"query":{"type":"string","minLength":1},
"max_results":{"type":"integer","minimum":1,"maximum":20},
"topic":{"type":"string","enum":["general","news","finance"]},
"time_range":{"type":"string","enum":["day","week","month","year"]}},
"required":["query"],"additionalProperties":false}`),
	}
}

type searchOutput struct {
	Query     string         `json:"query"`
	Results   []SearchResult `json:"results"`
	Truncated bool           `json:"truncated,omitempty"`
}

func (t searchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		Topic      string `json:"topic"`
		TimeRange  string `json:"time_range"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	// Out of range is refused rather than clamped: a model that asked for 50
	// should learn it cannot, not silently receive 20 and reason as though it
	// had asked for that.
	if input.MaxResults != 0 && (input.MaxResults < 1 || input.MaxResults > 20) {
		return nil, fmt.Errorf("max_results must be between 1 and 20")
	}
	if input.Topic != "" && !slices.Contains(knownTopics, input.Topic) {
		return nil, fmt.Errorf("topic must be one of general, news, finance")
	}
	if input.TimeRange != "" && !slices.Contains(knownTimeRanges, input.TimeRange) {
		return nil, fmt.Errorf("time_range must be one of day, week, month, year")
	}
	response, err := t.client.Search(ctx, SearchRequest{
		Query: input.Query, MaxResults: input.MaxResults, Topic: input.Topic, TimeRange: input.TimeRange,
	})
	if err != nil {
		return nil, err
	}
	output := searchOutput{Query: response.Query, Results: response.Results}
	share := shareOf(t.client.config.MaxOutputBytes, len(response.Results))
	for i, result := range output.Results {
		content, cut := truncate(result.Content, share)
		output.Results[i].Content = content
		output.Truncated = output.Truncated || cut
	}
	return json.Marshal(output)
}

type extractTool struct{ client *Client }

func (t extractTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "web_extract",
		Effect:      ports.ReadOnlyTool(),
		Description: "Read the actual content of web pages as markdown, given their urls -- this is how you read a page found by web_search. At most 5 urls per call. Long pages are truncated; a result marked truncated is a fragment, not the whole page. Pages that could not be read come back in failed rather than failing the call. query is optional and reranks which parts of each page are kept.",
		Schema: json.RawMessage(`{"type":"object","properties":{
"urls":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":5},
"query":{"type":"string"}},
"required":["urls"],"additionalProperties":false}`),
	}
}

type extractedPage struct {
	URL       string `json:"url"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

type extractOutput struct {
	Results []extractedPage  `json:"results"`
	Failed  []ExtractFailure `json:"failed"`
}

func (t extractTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		URLs  []string `json:"urls"`
		Query string   `json:"query"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	response, err := t.client.Extract(ctx, ExtractRequest{URLs: input.URLs, Query: input.Query})
	if err != nil {
		return nil, err
	}
	// A call where every url failed is still a successful call: "none of these
	// could be read" is a result the model can act on, not a tool error.
	output := extractOutput{Results: make([]extractedPage, 0, len(response.Results)), Failed: response.Failed}
	share := shareOf(t.client.config.MaxOutputBytes, len(response.Results))
	for _, result := range response.Results {
		content, cut := truncate(result.Content, share)
		output.Results = append(output.Results, extractedPage{URL: result.URL, Content: content, Truncated: cut})
	}
	return json.Marshal(output)
}
