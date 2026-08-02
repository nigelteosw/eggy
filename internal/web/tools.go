package web

import (
	"net/http"

	"github.com/nigelteosw/eggy/internal/kernel/services"
)

// ToolCatalog is the registry as the panel sees it: the merged catalog a turn
// would run on, read live. *services.ToolRegistry satisfies it directly, so
// the list is the same one the loop asks for rather than a second inventory
// assembled for display -- an MCP server that reconnected or was logged out of
// changes this page for the same reason it changes the next turn.
type ToolCatalog interface {
	Catalog() []services.ToolListing
}

// newToolListHandler answers with the same table shape the other list routes
// use. It is read-only on purpose: what tools exist is decided by config and
// by which MCP servers are connected, both of which have their own surface
// already. What the owner cannot see anywhere else is the merged result.
func newToolListHandler(tools ToolCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		headers := []string{"name", "source", "description"}
		if tools == nil {
			writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers})
			return
		}
		catalog := tools.Catalog()
		rows := make([][]string, 0, len(catalog))
		for _, listing := range catalog {
			rows = append(rows, []string{listing.Definition.Name, listing.Source, listing.Definition.Description})
		}
		writeWebResult(w, webResult{State: webSuccess, TableHeaders: headers, TableRows: rows})
	}
}
