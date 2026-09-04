package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ModelDiscoverer browses a provider's own model catalog on the owner's
// behalf. It exists so that writing a model alias does not require copying an
// ID out of a vendor's web page, which is where wrong IDs come from.
//
// It is deliberately not an allowlist and nothing here writes config: what
// Eggy will run stays exactly what the models section names. A discovered
// entry is a suggestion the owner may turn into an alias, and the alias is
// still what governs.
type ModelDiscoverer interface {
	// DiscoverableProviders lists the providers that opted in to discovery and
	// whose adapter can list. Providers absent from it are normal and working;
	// they simply cannot be browsed.
	DiscoverableProviders() []string
	DiscoverModels(ctx context.Context, provider string) ([]ports.CatalogModel, error)
}

// newModelDiscoveryHandler answers with the table shape the other list routes
// use, first column first: the model ID, which is the only cell that goes into
// an alias.
//
// The listing is returned whole rather than paged or pre-filtered. OpenRouter
// serves several hundred entries, so the panel filters client-side -- one
// round trip and instant typing beats a request per keystroke, and the
// provider's catalog is small enough in bytes to make that the easy choice.
func newModelDiscoveryHandler(discovery ModelDiscoverer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := []string{"Model", "Name", "Context"}
		provider := strings.TrimSpace(r.URL.Query().Get("provider"))
		if provider == "" {
			writeWebError(w, http.StatusBadRequest, "provider is required")
			return
		}
		if discovery == nil {
			writeWebError(w, http.StatusNotFound, "model discovery is unavailable")
			return
		}
		models, err := discovery.DiscoverModels(r.Context(), provider)
		if err != nil {
			// A provider that will not answer is the owner's own credential or
			// a service being down, not a bug in the panel, so the provider's
			// message is passed through rather than flattened.
			writeWebError(w, http.StatusBadGateway, err.Error())
			return
		}
		rows := make([][]string, 0, len(models))
		for _, model := range models {
			context := ""
			if model.ContextLength > 0 {
				context = strconv.FormatInt(model.ContextLength, 10)
			}
			rows = append(rows, []string{model.ID, model.Name, context})
		}
		writeWebResult(w, webResult{
			State: webSuccess, TableHeaders: headers, TableRows: rows,
			Detail: provider + " reports " + strconv.Itoa(len(rows)) + " models. Listing one does not enable it; add it as an alias to make it selectable.",
		})
	}
}
