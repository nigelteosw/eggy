package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetProviderAddsEntryAndRejectsInvalidURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetProvider(path, "openrouter", "openai_compatible", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := reloaded.Providers["openrouter"]
	if !ok || provider.BaseURL != "https://openrouter.ai/api/v1" || provider.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("openrouter provider = %#v, ok=%v", provider, ok)
	}
	if _, ok := reloaded.Providers["deepseek"]; !ok {
		t.Fatal("existing deepseek provider was dropped")
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = SetProvider(path, "broken", "openai_compatible", "not-a-url", "BROKEN_API_KEY")
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetModelAliasAddsEntryAndRejectsUnknownProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetModelAlias(path, "deepseek-fast", "deepseek", "deepseek-v4-flash", ""); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	model, ok := reloaded.ModelAliases["deepseek-fast"]
	if !ok || model.Provider != "deepseek" || model.Model != "deepseek-v4-flash" || len(model.ReasoningEfforts) != 0 {
		t.Fatalf("deepseek-fast model = %#v, ok=%v", model, ok)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = SetModelAlias(path, "orphan", "does-not-exist", "some-model", "")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetModelAliasAcceptsAndRejectsReasoningEfforts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetModelAlias(path, "deepseek-pro", "deepseek", "deepseek-v4-pro", "low,medium,high,max"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	model, ok := reloaded.ModelAliases["deepseek-pro"]
	if !ok || strings.Join(model.ReasoningEfforts, ",") != "low,medium,high,max" {
		t.Fatalf("deepseek-pro model = %#v, ok=%v", model, ok)
	}

	err = SetModelAlias(path, "deepseek-pro", "deepseek", "deepseek-v4-pro", "extreme")
	if err == nil || !strings.Contains(err.Error(), "invalid reasoning effort") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetMCPServerAddsNewServerWithSaneDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer(path, MCPServerInput{Name: "railway", URL: "https://mcp.railway.com", Auth: "oauth", BearerTokenEnv: "", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	env := testSecrets()
	env["RAILWAY_MCP_TOKEN"] = "unused"
	reloaded, _, err := LoadConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	server, ok := reloaded.MCP.Servers["railway"]
	if !ok || server.URL != "https://mcp.railway.com" || server.Auth != "oauth" || !server.Enabled {
		t.Fatalf("railway server = %#v, ok=%v", server, ok)
	}
	if server.Transport != "streamable-http" {
		t.Fatalf("transport = %q, want streamable-http", server.Transport)
	}
	if server.ConnectTimeout.Value() != 10*time.Second || server.Timeout.Value() != time.Minute || server.MaxOutputBytes != 128<<10 {
		t.Fatalf("defaults not applied: %#v", server)
	}
}

func TestSetMCPServerRefusesStdioServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
mcp:
  servers:
    filesystem:
      transport: stdio
      auth: none
      command: npx
      enabled: true
`
	before := []byte(body)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetMCPServer(path, MCPServerInput{Name: "filesystem", URL: "https://mcp.example.com", Auth: "none", BearerTokenEnv: "", Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "stdio transport") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetMCPServerPreservesToolFilterWhenEditingEssentialsOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
mcp:
  servers:
    railway:
      url: https://mcp.railway.com
      transport: streamable-http
      auth: bearer-env
      bearer_token_env: RAILWAY_MCP_TOKEN
      enabled: true
      tool_filter:
        include: [list-projects, get-logs]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only flip "enabled" through the essentials-only web form; tool_filter,
	// transport, and timeouts must survive untouched.
	if err := SetMCPServer(path, MCPServerInput{Name: "railway", URL: "https://mcp.railway.com", Auth: "bearer-env", BearerTokenEnv: "RAILWAY_MCP_TOKEN", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	env := testSecrets()
	env["RAILWAY_MCP_TOKEN"] = "unused"
	reloaded, _, err := LoadConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	server := reloaded.MCP.Servers["railway"]
	if server.Enabled {
		t.Fatal("expected enabled=false after edit")
	}
	if strings.Join(server.ToolFilter.Include, ",") != "list-projects,get-logs" {
		t.Fatalf("tool_filter.include was not preserved: %#v", server.ToolFilter)
	}
}

func TestSetMCPServerRejectsNonHTTPSURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfig())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetMCPServer(path, MCPServerInput{Name: "railway", URL: "http://mcp.railway.com", Auth: "oauth", BearerTokenEnv: "", Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestRemoveMCPServerDeletesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer(path, MCPServerInput{Name: "railway", URL: "https://mcp.railway.com", Auth: "oauth", BearerTokenEnv: "", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer(path, "railway"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.MCP.Servers["railway"]; ok {
		t.Fatal("expected railway server to be removed")
	}
}

func TestRemoveMCPServerRejectsUnknownName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfig())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := RemoveMCPServer(path, "does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestGetMCPServersConfigListsServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer(path, MCPServerInput{Name: "railway", URL: "https://mcp.railway.com", Auth: "oauth", BearerTokenEnv: "", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	servers, err := GetMCPServersConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers["railway"].URL != "https://mcp.railway.com" {
		t.Fatalf("servers = %#v", servers)
	}
}

func TestGetConfigTextFormatsEachSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := GetProvidersConfigText(path)
	if err != nil || providers != "deepseek  adapter=openai_compatible  base_url=https://api.deepseek.com  api_key_env=DEEPSEEK_API_KEY" {
		t.Fatalf("providers text = %q, err=%v", providers, err)
	}
	models, err := GetModelAliasesConfigText(path)
	if err != nil || models != "deepseek-pro  provider=deepseek  model=deepseek-v4-pro" {
		t.Fatalf("models text = %q, err=%v", models, err)
	}
}

func TestShowConfigTextDumpsWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := ShowConfigText(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"deepseek", "public_base_url"} {
		if !strings.Contains(text, want) {
			t.Fatalf("show text missing %q: %s", want, text)
		}
	}
}

// A hand-registered OAuth client is the only way to authorize against a server
// without dynamic client registration, so a surface must be able to set one --
// and must not drop it when a later edit changes something else.
func TestSetMCPServerKeepsPreRegisteredOAuthClientAcrossEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer(path, MCPServerInput{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1", Auth: "oauth",
		OAuthClientID: "eggy.apps.googleusercontent.com", OAuthClientSecretEnv: "GOOGLE_CLIENT_SECRET", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	servers, err := GetMCPServersConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if servers["calendar"].OAuthClientID != "eggy.apps.googleusercontent.com" || servers["calendar"].OAuthClientSecretEnv != "GOOGLE_CLIENT_SECRET" {
		t.Fatalf("server=%#v", servers["calendar"])
	}

	if err := SetMCPServer(path, MCPServerInput{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v2", Auth: "oauth", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	servers, _ = GetMCPServersConfig(path)
	if servers["calendar"].OAuthClientID == "" || servers["calendar"].OAuthClientSecretEnv == "" {
		t.Fatalf("editing the url dropped the registered client: %#v", servers["calendar"])
	}
	if servers["calendar"].URL != "https://calendarmcp.googleapis.com/mcp/v2" {
		t.Fatalf("server=%#v", servers["calendar"])
	}
}

// The client secret is named, never stored: a secret value in config.yaml is
// the one thing Eggy's configuration rules forbid outright.
func TestSetMCPServerRejectsOAuthClientCredentialsWithoutOAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetMCPServer(path, MCPServerInput{
		Name: "tickets", URL: "https://mcp.example.com", Auth: "none",
		OAuthClientID: "some-client", Enabled: true,
	})
	if err == nil {
		t.Fatal("oauth client credentials were accepted on a non-oauth server")
	}
	err = SetMCPServer(path, MCPServerInput{
		Name: "tickets", URL: "https://mcp.example.com", Auth: "oauth",
		OAuthClientSecretEnv: "not a variable name", OAuthClientID: "some-client", Enabled: true,
	})
	if err == nil {
		t.Fatal("an invalid environment variable name was accepted")
	}
}

// A stored oauth_client_secret_env that no longer validates must not make
// every later edit fail: the owner cannot see or clear that field from any
// surface, so an unusable value is dropped rather than carried forward.
func TestSetMCPServerRecoversFromAnUnusableStoredSecretEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `mcp:
  servers:
    calendar:
      url: "https://calendarmcp.googleapis.com/mcp/v1"
      transport: "streamable-http"
      auth: "oauth"
      oauth_client_id: "eggy.apps.googleusercontent.com"
      oauth_client_secret_env: "GOCSPX-not-a-variable-name"
      enabled: true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Editing something unrelated must succeed rather than fail on the stored
	// field the owner never sees.
	if err := SetMCPServer(path, MCPServerInput{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1", Auth: "oauth", Enabled: true,
	}); err != nil {
		t.Fatalf("edit blocked by an unusable stored value: %v", err)
	}
	servers, err := GetMCPServersConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if servers["calendar"].OAuthClientSecretEnv != "" {
		t.Fatalf("unusable value survived: %#v", servers["calendar"])
	}

	// And supplying a good one still works.
	if err := SetMCPServer(path, MCPServerInput{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1", Auth: "oauth",
		OAuthClientID: "eggy.apps.googleusercontent.com", OAuthClientSecretEnv: "GOOGLE_CLIENT_SECRET", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	servers, _ = GetMCPServersConfig(path)
	if servers["calendar"].OAuthClientSecretEnv != "GOOGLE_CLIENT_SECRET" {
		t.Fatalf("server=%#v", servers["calendar"])
	}
}

func TestSetGooglePreservesWhatASurfaceCannotSee(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
google:
  enabled: true
  client_id: "old.apps.googleusercontent.com"
  client_secret_env: "GOOGLE_CLIENT_SECRET"
  products: ["calendar"]
  scopes: ["https://www.googleapis.com/auth/calendar.readonly"]
  max_output_bytes: 4096
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetGoogle(path, GoogleInput{Enabled: true, Products: []string{"gmail", "calendar"}}); err != nil {
		t.Fatal(err)
	}
	env := testSecrets()
	env["GOOGLE_CLIENT_SECRET"] = "secret"
	reloaded, _, err := LoadConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	google := reloaded.Google
	// A surface edit that blanked the client id, the narrowed scopes, or the
	// output bound would be a config the owner never asked for.
	if google.ClientID != "old.apps.googleusercontent.com" || google.ClientSecretEnv != "GOOGLE_CLIENT_SECRET" {
		t.Fatalf("client fields lost: %#v", google)
	}
	if len(google.Scopes) != 1 || google.MaxOutputBytes != 4096 {
		t.Fatalf("advanced fields lost: %#v", google)
	}
	if len(google.Products) != 2 || google.Products[0] != "calendar" || google.Products[1] != "gmail" {
		t.Fatalf("products=%v, want the new set sorted", google.Products)
	}
}

// A rejected config is never written: the owner still has the file they had.
func TestSetGoogleRejectsAnUnknownProduct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetGoogle(path, GoogleInput{Enabled: true, ClientID: "x.apps.googleusercontent.com", Products: []string{"gmial"}})
	if err == nil || !strings.Contains(err.Error(), "unknown google product") {
		t.Fatalf("error=%v", err)
	}
	stored, err := GetGoogleConfig(path)
	if err != nil || stored.Enabled {
		t.Fatalf("a rejected write reached the file: %#v err=%v", stored, err)
	}
}

// Disabling must not require re-sending the client, or an owner turning Google
// off from a phone would erase the configuration needed to turn it back on.
func TestSetGoogleDisablesWithoutErasing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetGoogle(path, GoogleInput{Enabled: true, ClientID: "x.apps.googleusercontent.com", Products: []string{"calendar"}}); err != nil {
		t.Fatal(err)
	}
	if err := SetGoogle(path, GoogleInput{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	stored, err := GetGoogleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || stored.ClientID != "x.apps.googleusercontent.com" || len(stored.Products) != 1 {
		t.Fatalf("stored=%#v", stored)
	}
}
