package bootstrap

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/search/googlecse"
	"github.com/nigelteosw/eggy/plugins/search/searxng"
	"github.com/nigelteosw/eggy/plugins/search/tavily"
)

type fakeWebSearcher struct{}

func (fakeWebSearcher) Search(_ context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	return []ports.WebSearchResult{{
		Title: "Fake web search result",
		URL:   "https://example.com/search?q=" + url.QueryEscape(request.Query),
	}}, nil
}

func newWebSearcher(config Config, secrets Secrets, options AppOptions) (ports.WebSearcher, error) {
	if !webSearchConfigured(config, secrets) {
		return nil, nil
	}
	if options.FakeAdapters {
		return fakeWebSearcher{}, nil
	}
	switch config.WebSearch.Adapter {
	case "searxng":
		return searxng.New(searxng.Config{
			BaseURL:    secrets.WebSearchBaseURL,
			Timeout:    config.WebSearch.Timeout.Value(),
			SafeSearch: config.WebSearch.SafeSearchValue(),
			MaxBytes:   1 << 20,
		}, options.HTTPClient)
	case "google_cse":
		return googlecse.New(googlecse.Config{
			Endpoint:   secrets.WebSearchBaseURL,
			APIKey:     secrets.WebSearchAPIKey,
			EngineID:   secrets.WebSearchEngineID,
			Timeout:    config.WebSearch.Timeout.Value(),
			SafeSearch: config.WebSearch.SafeSearchValue(),
			MaxBytes:   1 << 20,
		}, options.HTTPClient)
	case "tavily":
		return tavily.New(tavily.Config{
			Endpoint:    secrets.WebSearchBaseURL,
			APIKey:      secrets.WebSearchAPIKey,
			SearchDepth: config.WebSearch.SearchDepth,
			Timeout:     config.WebSearch.Timeout.Value(),
			MaxBytes:    1 << 20,
		}, options.HTTPClient)
	default:
		return nil, fmt.Errorf("unsupported web search adapter %q", config.WebSearch.Adapter)
	}
}

// webSearchConfigured reports whether the selected adapter has the secrets it
// needs. Web search stays absent rather than failing startup when it does not.
func webSearchConfigured(config Config, secrets Secrets) bool {
	switch config.WebSearch.Adapter {
	case "google_cse":
		return strings.TrimSpace(secrets.WebSearchAPIKey) != "" && strings.TrimSpace(secrets.WebSearchEngineID) != ""
	case "tavily":
		return strings.TrimSpace(secrets.WebSearchAPIKey) != ""
	default:
		return strings.TrimSpace(secrets.WebSearchBaseURL) != ""
	}
}
