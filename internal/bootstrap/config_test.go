package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigResolvesWebUICredentialsAndRequiresEncryptionKeyWhenSet(t *testing.T) {
	// validConfig() enables Calendar by default, which already requires
	// EGGY_ENCRYPTION_KEY for an unrelated reason. Disable it here so this
	// test isolates the new web UI credential check specifically.
	body := strings.Replace(validConfig(), "enabled: true\n  default_calendar", "enabled: false\n  default_calendar", 1)
	env := testSecrets()
	delete(env, "EGGY_ENCRYPTION_KEY")

	if _, secrets, err := loadText(t, body, env); err != nil {
		t.Fatalf("unconfigured web UI must not block boot: %v", err)
	} else if secrets.UIUserEmail != "" || secrets.UIPassword != "" {
		t.Fatalf("expected empty web UI credentials, got %#v", secrets)
	}

	env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
	env["EGGY_UI_PASSWORD"] = "hunter2"
	if _, _, err := loadText(t, body, env); err == nil {
		t.Fatal("expected error: web UI configured without EGGY_ENCRYPTION_KEY")
	}

	env["EGGY_ENCRYPTION_KEY"] = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	_, secrets, err := loadText(t, body, env)
	if err != nil {
		t.Fatalf("fully configured web UI must load: %v", err)
	}
	if secrets.UIUserEmail != "owner@example.com" || secrets.UIPassword != "hunter2" {
		t.Fatalf("secrets=%#v", secrets)
	}
}

func TestLoadConfigAcceptsExample(t *testing.T) {
	env := testSecrets()
	cfg, secrets, err := LoadConfig(filepath.Join("..", "..", "config.example.yaml"), mapEnv(env))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Telegram.OwnerID != 123456789 || cfg.Agent.DefaultModel != "deepseek-pro" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.Runner.Timeout.Value() != 45*time.Minute || cfg.Server.Listen != ":8080" {
		t.Fatalf("defaults/durations not loaded: %#v", cfg)
	}
	if secrets.ProviderAPIKeys["deepseek"] != env["DEEPSEEK_API_KEY"] {
		t.Fatal("provider secret was not loaded")
	}
}

func TestLoadConfigAcceptsRailwayMCP(t *testing.T) {
	env := testSecrets()
	env["RAILWAY_MCP_TOKEN"] = "railway-token"
	cfg, secrets, err := loadText(t, validConfig()+`
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
`, env)
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCP.Servers["railway"]
	if server.ConnectTimeout.Value() != 10*time.Second || server.Timeout.Value() != time.Minute || server.MaxOutputBytes != 128<<10 {
		t.Fatalf("server defaults = %#v", server)
	}
	if secrets.MCPBearerTokens["railway"] != "railway-token" {
		t.Fatalf("MCP bearer secrets = %#v", secrets.MCPBearerTokens)
	}
}

func TestMCPConfigValidation(t *testing.T) {
	base := validConfig() + `
mcp:
  servers:
    railway:
      url: https://mcp.railway.com
      transport: streamable-http
      auth: oauth
      enabled: true
`
	tests := []struct{ name, old, replacement, want string }{
		{"https", "https://mcp.railway.com", "http://remote.test", "must use HTTPS"},
		{"credentials in URL", "https://mcp.railway.com", "https://token@mcp.railway.com", "must not contain credentials"},
		{"transport", "streamable-http", "stdio", "unsupported transport"},
		{"auth", "auth: oauth", "auth: token", "unsupported auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := loadText(t, strings.Replace(base, tt.old, tt.replacement, 1), testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestMCPBearerEnvRequiresCredential(t *testing.T) {
	body := validConfig() + `
mcp:
  servers:
    railway:
      url: https://mcp.railway.com
      transport: streamable-http
      auth: bearer-env
      bearer_token_env: RAILWAY_MCP_TOKEN
      enabled: true
`
	_, _, err := loadText(t, body, testSecrets())
	if err == nil || !strings.Contains(err.Error(), "RAILWAY_MCP_TOKEN") {
		t.Fatalf("error=%v", err)
	}
}

func TestMCPFilterAllowsExcludeToOverrideInclude(t *testing.T) {
	body := validConfig() + `
mcp:
  servers:
    example:
      url: https://mcp.example.com
      transport: streamable-http
      auth: none
      enabled: true
      tool_filter:
        include: [read, sensitive]
        exclude: [sensitive]
`
	if _, _, err := loadText(t, body, testSecrets()); err != nil {
		t.Fatalf("exclude should be allowed to override include: %v", err)
	}
}

func TestEnabledMCPOAuthRequiresEncryptionKey(t *testing.T) {
	body := strings.Replace(validConfig(), "enabled: true\n  default_calendar", "enabled: false\n  default_calendar", 1) + `
mcp:
  servers:
    example:
      url: https://mcp.example.com
      transport: streamable-http
      auth: oauth
      enabled: true
`
	env := testSecrets()
	delete(env, "EGGY_ENCRYPTION_KEY")
	_, _, err := loadText(t, body, env)
	if err == nil || !strings.Contains(err.Error(), "EGGY_ENCRYPTION_KEY") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadConfigNormalizesProvidersAndModels(t *testing.T) {
	cfg, secrets, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	provider, model, err := cfg.ActiveModel("deepseek-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.DefaultModel != "deepseek-pro" || provider.Adapter != "openai_compatible" || provider.BaseURL != "https://api.deepseek.com" || model.Model != "deepseek-v4-pro" {
		t.Fatalf("normalized config = %#v provider=%#v model=%#v", cfg, provider, model)
	}
	if secrets.ProviderAPIKeys["deepseek"] != "deepseek-key" {
		t.Fatalf("provider secrets = %#v", secrets.ProviderAPIKeys)
	}
}

func TestEmbeddingConfigIsOptionalAndDefaultsCandidateLimit(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatalf("config without embeddings: %v", err)
	}
	if cfg.Embeddings != (EmbeddingsConfig{}) {
		t.Fatalf("absent embeddings=%#v", cfg.Embeddings)
	}

	cfg, _, err = loadText(t, validConfig()+`
embeddings:
  provider: deepseek
  model: text-embedding-3-small
  dimensions: 1536
`, testSecrets())
	if err != nil {
		t.Fatalf("configured embeddings: %v", err)
	}
	if cfg.Embeddings != (EmbeddingsConfig{Provider: "deepseek", Model: "text-embedding-3-small", Dimensions: 1536, CandidateLimit: 5000}) {
		t.Fatalf("embeddings=%#v", cfg.Embeddings)
	}
}

func TestEmbeddingConfigValidation(t *testing.T) {
	tests := []struct {
		name, document, want string
	}{
		{"unknown provider", "provider: missing\n  model: text-embedding-3-small\n  dimensions: 1536", "unknown provider"},
		{"unsupported provider adapter", "provider: deepseek\n  model: text-embedding-3-small\n  dimensions: 1536", "unsupported provider adapter"},
		{"missing model", "provider: deepseek\n  model: ''\n  dimensions: 1536", "embeddings.model"},
		{"non-positive dimensions", "provider: deepseek\n  model: text-embedding-3-small\n  dimensions: 0", "embeddings.dimensions"},
		{"negative candidate limit", "provider: deepseek\n  model: text-embedding-3-small\n  dimensions: 1536\n  candidate_limit: -1", "embeddings.candidate_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := validConfig() + "\nembeddings:\n  " + tt.document + "\n"
			if tt.name == "unsupported provider adapter" {
				body = strings.Replace(body, "adapter: openai_compatible", "adapter: unsupported", 1)
			}
			_, _, err := loadText(t, body, testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProviderValidation(t *testing.T) {
	tests := []struct {
		name, old, replacement, want string
	}{
		{"adapter", "adapter: openai_compatible", "adapter: native", "unsupported provider adapter"},
		{"base URL", "base_url: https://api.deepseek.com", "base_url: ftp://bad", "base_url"},
		{"key env", "api_key_env: DEEPSEEK_API_KEY", "api_key_env: bad-key", "api_key_env"},
		{"missing provider", "provider: deepseek", "provider: missing", "unknown provider"},
		{"missing default", "default_model: deepseek-pro", "default_model: missing", "agent.default_model"},
		{"empty model", "model: deepseek-v4-pro", "model: ''", "model is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := loadText(t, strings.Replace(validConfig(), tt.old, tt.replacement, 1), testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want containing %q", err, tt.want)
			}
		})
	}
	t.Run("missing provider credential", func(t *testing.T) {
		env := testSecrets()
		delete(env, "DEEPSEEK_API_KEY")
		_, _, err := loadText(t, validConfig(), env)
		if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestLoadConfigUsesValidatedRuntimePort(t *testing.T) {
	tests := []struct {
		port       string
		wantListen string
		wantError  string
	}{
		{"4317", ":4317", ""},
		{"", ":8080", ""},
		{"0", "", "PORT must be an integer between 1 and 65535"},
		{"65536", "", "PORT must be an integer between 1 and 65535"},
		{"http", "", "PORT must be an integer between 1 and 65535"},
	}
	for _, tt := range tests {
		t.Run(tt.port, func(t *testing.T) {
			env := testSecrets()
			env["PORT"] = tt.port
			cfg, _, err := loadText(t, validConfig(), env)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Server.Listen != tt.wantListen {
				t.Fatalf("server listen = %q, want %q", cfg.Server.Listen, tt.wantListen)
			}
		})
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	_, _, err := loadText(t, validConfig()+"unknown: true\n", testSecrets())
	if err == nil || !strings.Contains(err.Error(), "field unknown") {
		t.Fatalf("expected strict YAML error, got %v", err)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		rewrite func(string) string
		want    string
	}{
		{"owner", func(s string) string { return strings.Replace(s, "owner_id: 42", "owner_id: 0", 1) }, "telegram.owner_id"},
		{"base URL", func(s string) string {
			return strings.Replace(s, "public_base_url: https://eggy.example", "public_base_url: ftp://bad", 1)
		}, "server.public_base_url"},
		{"duplicate repository", func(s string) string {
			return strings.Replace(s, "runner:\n", "  - name: repo\n    clone_url: https://github.com/acme/other.git\n    base_branch: main\nrunner:\n", 1)
		}, "duplicate repository"},
		{"protected base", func(s string) string {
			return strings.Replace(s, "protected_branches: [main]", "protected_branches: [main, 'bad branch']", 1)
		}, "protected branch"},
		{"duration", func(s string) string { return strings.Replace(s, "timeout: 5m", "timeout: forever", 1) }, "invalid duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := loadText(t, tt.rewrite(validConfig()), testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestConfigRejectsRunnerRootOutsideDataDir(t *testing.T) {
	_, _, err := loadText(t, strings.Replace(validConfig(), "root: /data/runs", "root: /other/runs", 1), testSecrets())
	if err == nil || !strings.Contains(err.Error(), "runner.root must be within data_dir") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadConfigDefaultsImplementationSessionPolicy(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImplementationSessions.ContextBudgetChars != 96000 || cfg.ImplementationSessions.RecentMessages != 16 || cfg.ImplementationSessions.OutputExcerptChars != 8192 {
		t.Fatalf("implementation sessions=%#v", cfg.ImplementationSessions)
	}
}

func TestLoadConfigRequiresSecretsForEnabledCapabilities(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN"},
		{"TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_WEBHOOK_SECRET"},
		{"DEEPSEEK_API_KEY", "DEEPSEEK_API_KEY"},
		{"GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID"},
		{"GOOGLE_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET"},
		{"EGGY_ENCRYPTION_KEY", "EGGY_ENCRYPTION_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			env := testSecrets()
			delete(env, tt.key)
			_, _, err := loadText(t, validConfig(), env)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected missing %s, got %v", tt.want, err)
			}
		})
	}
}

func TestDotEnvUsesFileFallbackWithoutOverridingProcessEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# local secrets\nDEEPSEEK_API_KEY=file-key\nQUOTED=\"hello world\"\nEXISTING=file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv, err := DotEnv(path, func(key string) string {
		if key == "EXISTING" {
			return "process-value"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if getenv("DEEPSEEK_API_KEY") != "file-key" || getenv("QUOTED") != "hello world" || getenv("EXISTING") != "process-value" {
		t.Fatalf("unexpected environment values")
	}
	if _, err := DotEnv(filepath.Join(t.TempDir(), "missing"), func(string) string { return "" }); err != nil {
		t.Fatalf("missing optional .env: %v", err)
	}
}

func loadText(t *testing.T, body string, env map[string]string) (Config, Secrets, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path, mapEnv(env))
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func testSecrets() map[string]string {
	return map[string]string{
		"TELEGRAM_BOT_TOKEN":      "telegram-token",
		"TELEGRAM_WEBHOOK_SECRET": "webhook-secret",
		"DEEPSEEK_API_KEY":        "deepseek-key",
		"GITHUB_TOKEN":            "github-token",
		"GOOGLE_CLIENT_ID":        "google-client",
		"GOOGLE_CLIENT_SECRET":    "google-secret",
		"EGGY_ENCRYPTION_KEY":     "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
}

func validConfig() string {
	return `
server:
  listen: ':8080'
  public_base_url: https://eggy.example
  telegram_webhook_path: /webhooks/telegram
data_dir: /data
telegram:
  owner_id: 42
agent:
  default_model: deepseek-pro
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro
repositories:
  - name: repo
    clone_url: https://github.com/acme/repo.git
    base_branch: main
    protected_branches: [main]
runner:
  root: /data/runs
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
scheduler:
  heartbeat_cadence: 30m
  quiet_hours:
    start: '22:00'
    end: '07:00'
    timezone: UTC
  minimum_proactive_interval: 2h
  weekly_proactive_limit: 3
calendar:
  enabled: true
  default_calendar: primary
  timezone: UTC
`
}
