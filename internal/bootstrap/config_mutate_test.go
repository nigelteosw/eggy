package bootstrap

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

func TestSetCalendarPatchesOnlyGivenFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	// validConfigV2 already has calendar.enabled=true, default_calendar=primary, timezone=UTC.
	if err := SetCalendar(path, "", "", "Asia/Singapore"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Calendar.Enabled || reloaded.Calendar.DefaultCalendar != "primary" || reloaded.Calendar.Timezone != "Asia/Singapore" {
		t.Fatalf("calendar = %#v", reloaded.Calendar)
	}

	if err := SetCalendar(path, "false", "", ""); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err = LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Calendar.Enabled || reloaded.Calendar.DefaultCalendar != "primary" || reloaded.Calendar.Timezone != "Asia/Singapore" {
		t.Fatalf("calendar after disabling = %#v", reloaded.Calendar)
	}
}

func TestSetCalendarRequiresAtLeastOneField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfig())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetCalendar(path, "", "", "")
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetCalendarRejectsInvalidBool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfig())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetCalendar(path, "not-a-bool", "", "")
	if err == nil || !strings.Contains(err.Error(), "enabled must be true or false") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetMCPServerAddsNewServerWithSaneDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer(path, "railway", "https://mcp.railway.com", "oauth", "", true); err != nil {
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
	if err := SetMCPServer(path, "railway", "https://mcp.railway.com", "bearer-env", "RAILWAY_MCP_TOKEN", false); err != nil {
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
	err := SetMCPServer(path, "railway", "http://mcp.railway.com", "oauth", "", true)
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
	if err := SetMCPServer(path, "railway", "https://mcp.railway.com", "oauth", "", true); err != nil {
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
	if err := SetMCPServer(path, "railway", "https://mcp.railway.com", "oauth", "", true); err != nil {
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
	calendar, err := GetCalendarConfigText(path)
	if err != nil || calendar != "enabled=true  default_calendar=primary  timezone=UTC" {
		t.Fatalf("calendar text = %q, err=%v", calendar, err)
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
	for _, want := range []string{"deepseek", "public_base_url", "calendar"} {
		if !strings.Contains(text, want) {
			t.Fatalf("show text missing %q: %s", want, text)
		}
	}
}
