package tavily

import (
	"context"
	"errors"
	"strings"
)

// SearchRequest is provider-neutral on purpose: it names what a caller wants,
// not what Tavily's body looks like. The wire shape below is what would be
// rewritten for a second provider.
type SearchRequest struct {
	Query      string
	MaxResults int
	Topic      string
	TimeRange  string
}

type SearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

type SearchResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
}

// searchBody is everything Eggy sends. The fields left out are left out
// deliberately: include_answer asks Tavily to summarize, which is the model's
// job once it has the sources; include_images returns URLs no surface can
// render; include_raw_content would fold extraction into search, which is what
// web_extract exists to do explicitly and under its own budget.
type searchBody struct {
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth"`
	MaxResults  int    `json:"max_results"`
	Topic       string `json:"topic,omitempty"`
	TimeRange   string `json:"time_range,omitempty"`
}

func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return SearchResponse{}, errors.New("query must not be empty")
	}
	limit := request.MaxResults
	if limit <= 0 {
		limit = c.config.MaxResults
	}
	body := searchBody{
		Query:       query,
		SearchDepth: c.config.SearchDepth,
		MaxResults:  limit,
		Topic:       request.Topic,
		TimeRange:   request.TimeRange,
	}
	var response SearchResponse
	if err := c.post(ctx, "/search", body, &response); err != nil {
		return SearchResponse{}, err
	}
	if response.Results == nil {
		response.Results = []SearchResult{}
	}
	response.Query = query
	return response, nil
}
