package tavily

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ExtractRequest struct {
	URLs  []string
	Query string
}

type ExtractResult struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type ExtractFailure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type ExtractResponse struct {
	Results []ExtractResult  `json:"results"`
	Failed  []ExtractFailure `json:"failed"`
}

type extractBody struct {
	URLs         []string `json:"urls"`
	Query        string   `json:"query,omitempty"`
	ExtractDepth string   `json:"extract_depth"`
	Format       string   `json:"format"`
}

// extractWire is Tavily's response shape, kept here rather than exported so
// the field names it happens to use (raw_content, failed_results) do not reach
// the tools.
type extractWire struct {
	Results []struct {
		URL        string `json:"url"`
		RawContent string `json:"raw_content"`
	} `json:"results"`
	FailedResults []struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	} `json:"failed_results"`
}

func (c *Client) Extract(ctx context.Context, request ExtractRequest) (ExtractResponse, error) {
	urls := make([]string, 0, len(request.URLs))
	for _, raw := range request.URLs {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	if len(urls) == 0 {
		return ExtractResponse{}, errors.New("urls must name at least one page")
	}
	if len(urls) > maxExtractURLs {
		return ExtractResponse{}, fmt.Errorf("urls must name at most %d pages per call", maxExtractURLs)
	}
	body := extractBody{
		URLs:         urls,
		Query:        strings.TrimSpace(request.Query),
		ExtractDepth: c.config.ExtractDepth,
		Format:       "markdown",
	}
	var wire extractWire
	if err := c.post(ctx, "/extract", body, &wire); err != nil {
		return ExtractResponse{}, err
	}
	response := ExtractResponse{
		Results: make([]ExtractResult, 0, len(wire.Results)),
		Failed:  make([]ExtractFailure, 0, len(wire.FailedResults)),
	}
	for _, result := range wire.Results {
		response.Results = append(response.Results, ExtractResult{URL: result.URL, Content: result.RawContent})
	}
	for _, failure := range wire.FailedResults {
		response.Failed = append(response.Failed, ExtractFailure{URL: failure.URL, Error: failure.Error})
	}
	return response, nil
}
