// The panel's MCP administration. Config edits go through internal/config like
// every other section; starting an OAuth flow is the one thing a config file
// cannot do, so it is the only route here that needs the running manager.
package web

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/nigelteosw/eggy/internal/config"
)

func webMCPListRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		servers, err := config.GetMCPServersConfig(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		names := slices.Sorted(maps.Keys(servers))
		rows := make([][]string, 0, len(names))
		for _, name := range names {
			server := servers[name]
			// Only names are ever rendered, never values: bearer_token_env and
			// oauth_client_secret_env are variable names, and the client id is
			// public by construction. No secret reaches this response.
			rows = append(rows, []string{name, server.Transport, server.URL, server.Auth, strconv.FormatBool(server.Enabled), server.BearerTokenEnv, server.OAuthClientID, server.OAuthClientSecretEnv})
		}
		writeWebResult(w, webResult{
			State:        webSuccess,
			TableHeaders: []string{"Name", "Transport", "URL", "Auth", "Enabled", "Bearer token env", "OAuth client id", "OAuth client secret env"},
			TableRows:    rows,
		})
	}
}

func webMCPSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var values config.Values
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		name := values["name"]
		if name == "" || values["url"] == "" || values["auth"] == "" {
			writeWebError(w, http.StatusBadRequest, "name, url, and auth are required")
			return
		}
		input, decodeErr := values.MCPServerInput(name)
		if decodeErr != nil {
			writeWebError(w, http.StatusBadRequest, decodeErr.Error())
			return
		}
		if err := config.SetMCPServer(configPath, input); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved MCP server " + name + ".", Detail: restartToApply})
	}
}

// webMCPLoginRoute starts an OAuth flow and redirects the owner's browser to
// the provider. Without it the callback route below is unreachable: nothing
// else in the process calls BeginLogin, so an auth: oauth server could be
// configured but never authorized.
func webMCPLoginRoute(runtime MCPLoginStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server := r.PathValue("server")
		if server == "" {
			writeWebError(w, http.StatusBadRequest, "server name is required")
			return
		}
		authorizationURL, err := runtime.BeginLogin(r.Context(), server)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, authorizationURL, http.StatusFound)
	}
}

func webMCPRemoveRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			writeWebError(w, http.StatusBadRequest, "server name is required")
			return
		}
		if err := config.RemoveMCPServer(configPath, name); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Removed MCP server " + name + ".", Detail: restartToApply})
	}
}
