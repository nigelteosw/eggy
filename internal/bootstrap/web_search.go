package bootstrap

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/nigelteosw/eggy/internal/adapters/search/searxng"
	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeWebSearcher struct{}

func (fakeWebSearcher) Search(_ context.Context, request ports.WebSearchRequest) ([]ports.WebSearchResult, error) {
	return []ports.WebSearchResult{{
		Title: "Fake web search result",
		URL:   "https://example.com/search?q=" + url.QueryEscape(request.Query),
	}}, nil
}

func newWebSearcher(config Config, secrets Secrets, options AppOptions) (ports.WebSearcher, error) {
	if strings.TrimSpace(secrets.WebSearchBaseURL) == "" {
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
	default:
		return nil, fmt.Errorf("unsupported web search adapter %q", config.WebSearch.Adapter)
	}
}
