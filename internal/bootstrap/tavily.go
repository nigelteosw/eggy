package bootstrap

import (
	"net/http"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
	tavilyadapter "github.com/nigelteosw/eggy/plugins/tools/tavily"
)

// newTavilyTools returns nil when Tavily is not configured, which is what
// makes an absent capability cost nothing: no client is built, no tool is
// registered, and the two schemas never reach a model request.
func newTavilyTools(cfg config.Config, secrets config.Secrets, options AppOptions) []ports.Tool {
	if !cfg.Tavily.Enabled {
		return nil
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return tavilyadapter.NewTools(tavilyadapter.New(client, tavilyadapter.Config{
		APIKey:         secrets.TavilyAPIKey,
		APIKeyEnv:      cfg.Tavily.APIKeyEnv,
		SearchDepth:    cfg.Tavily.SearchDepth,
		ExtractDepth:   cfg.Tavily.ExtractDepth,
		MaxResults:     cfg.Tavily.MaxResults,
		MaxOutputBytes: cfg.Tavily.MaxOutputBytes,
		Timeout:        cfg.Tavily.Timeout.Value(),
	}))
}
