package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nigelteosw/eggy/internal/config"
)

// ErrMCPRestartRequired means the server was added to config after boot and the
// owner must restart before it can be previewed.
var ErrMCPRestartRequired = errors.New("restart to load this server")

// ErrMCPStdioNotPreviewed means the server runs a local program, which a review
// must not start.
var ErrMCPStdioNotPreviewed = errors.New("stdio servers are not previewed; enable them in config.yaml")

// ErrMCPLoginRequired means the server needs an OAuth login before it can be
// previewed.
var ErrMCPLoginRequired = errors.New("login required")

// MCPToolPreview is one tool a server advertises, for the enable review.
type MCPToolPreview struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPPreviewer lists a configured server's tools without registering them. The
// panel declares it so it never imports the MCP adapter.
type MCPPreviewer interface {
	Preview(ctx context.Context, name string) ([]MCPToolPreview, error)
}

// registerMCPApprovalRoutes mounts the enable-with-approvals routes. The approval
// write is config, so it is always mounted; the preview needs the running
// manager, so it is mounted only when one exists.
func registerMCPApprovalRoutes(mux *http.ServeMux, guard func(http.HandlerFunc) http.Handler, configPath string, previewer MCPPreviewer) {
	mux.Handle("PUT /api/config/mcp/{name}/approval", guard(mcpApprovalRoute(configPath)))
	if previewer != nil {
		mux.Handle("POST /api/config/mcp/{name}/preview", guard(mcpPreviewRoute(previewer)))
	}
}

func mcpApprovalRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled         bool     `json:"enabled"`
			RequireApproval []string `json:"require_approval"`
		}
		if err := decodeMarketplaceBody(w, r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.SetMCPServerApproval(configPath, r.PathValue("name"), body.Enabled, body.RequireApproval); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"saved": true})
	}
}

func mcpPreviewRoute(previewer MCPPreviewer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tools, err := previewer.Preview(r.Context(), r.PathValue("name"))
		switch {
		case errors.Is(err, ErrMCPRestartRequired):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"restart": true, "error": err.Error()})
			return
		case errors.Is(err, ErrMCPLoginRequired):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"login": true, "error": err.Error()})
			return
		case errors.Is(err, ErrMCPStdioNotPreviewed):
			writeWebError(w, http.StatusConflict, err.Error())
			return
		case err != nil:
			writeWebError(w, http.StatusBadGateway, "could not reach the MCP server")
			return
		}
		if tools == nil {
			tools = []MCPToolPreview{}
		}
		writeJSON(w, map[string]any{"tools": tools})
	}
}
