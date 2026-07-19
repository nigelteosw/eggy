package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

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

func TestLoadConfigVersion2(t *testing.T) {
	cfg, secrets, err := loadText(t, validConfigV2(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	provider, model, err := cfg.ActiveModel("deepseek-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 2 || cfg.Agent.DefaultModel != "deepseek-pro" || provider.Adapter != "openai_compatible" || provider.BaseURL != "https://api.deepseek.com" || model.Model != "deepseek-v4-pro" {
		t.Fatalf("normalized config = %#v provider=%#v model=%#v", cfg, provider, model)
	}
	if secrets.ProviderAPIKeys["deepseek"] != "deepseek-key" {
		t.Fatalf("provider secrets = %#v", secrets.ProviderAPIKeys)
	}
}

func TestLoadConfigVersion1Compatibility(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	provider, model, err := cfg.ActiveModel("deepseek-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.DefaultModel != "deepseek-pro" || provider.APIKeyEnv != "DEEPSEEK_API_KEY" || model.Model != "pro" {
		t.Fatalf("v1 normalization = %#v provider=%#v model=%#v", cfg, provider, model)
	}
}

func TestCodingConfigDefaultsForVersion1AndOmittedVersion2(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "version 1", body: validConfig()},
		{name: "version 2", body: validConfigV2()},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, secrets, err := loadText(t, test.body, testSecrets())
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Coding.DefaultAgent != "codex" || len(cfg.Coding.Agents) != 1 || cfg.Coding.Agents["codex"].Adapter != "codex_cli" {
				t.Fatalf("coding config = %#v", cfg.Coding)
			}
			if len(secrets.CodingAgentCredentials) != 0 {
				t.Fatalf("coding agent credentials = %#v", secrets.CodingAgentCredentials)
			}
		})
	}
}

func TestCodingConfigLoadsCredentialsWithoutPersistingThem(t *testing.T) {
	body := strings.Replace(validConfigV2(), "repositories:", `coding:
  default_agent: codex
  agents:
    codex:
      adapter: codex_cli
    claude:
      adapter: claude_cli
      credential_env: CLAUDE_CODE_OAUTH_TOKEN
repositories:`, 1)
	env := testSecrets()
	env["CLAUDE_CODE_OAUTH_TOKEN"] = "secret-claude-token"
	cfg, secrets, err := loadText(t, body, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Coding.DefaultAgent != "codex" || cfg.Coding.Agents["claude"].CredentialEnv != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("coding config = %#v", cfg.Coding)
	}
	if secrets.CodingAgentCredentials["claude"] != "secret-claude-token" {
		t.Fatalf("coding agent credentials = %#v", secrets.CodingAgentCredentials)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-claude-token")) {
		t.Fatalf("secret persisted in config:\n%s", encoded)
	}
}

func TestCodingConfigValidation(t *testing.T) {
	base := strings.Replace(validConfigV2(), "repositories:", `coding:
  default_agent: codex
  agents:
    codex:
      adapter: codex_cli
    claude:
      adapter: claude_cli
      credential_env: CLAUDE_CODE_OAUTH_TOKEN
repositories:`, 1)
	tests := []struct {
		name, old, replacement, want string
	}{
		{"unsupported adapter", "adapter: claude_cli", "adapter: shell", "unsupported coding agent adapter"},
		{"invalid credential environment", "credential_env: CLAUDE_CODE_OAUTH_TOKEN", "credential_env: bad-token", "credential_env"},
		{"invalid alias", "    claude:", "    'bad alias':", "invalid coding agent alias"},
		{"unknown default", "default_agent: codex", "default_agent: missing", "coding.default_agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := loadText(t, strings.Replace(base, tt.old, tt.replacement, 1), testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestCodingConfigRequiresOnlyDefaultAgentCredential(t *testing.T) {
	base := strings.Replace(validConfigV2(), "repositories:", `coding:
  default_agent: codex
  agents:
    codex:
      adapter: codex_cli
    claude:
      adapter: claude_cli
      credential_env: CLAUDE_CODE_OAUTH_TOKEN
repositories:`, 1)
	if _, secrets, err := loadText(t, base, testSecrets()); err != nil {
		t.Fatalf("missing optional credential rejected: %v", err)
	} else if value, ok := secrets.CodingAgentCredentials["claude"]; !ok || value != "" {
		t.Fatalf("optional credential loading = %#v", secrets.CodingAgentCredentials)
	}

	claudeDefault := strings.Replace(base, "default_agent: codex", "default_agent: claude", 1)
	_, _, err := loadText(t, claudeDefault, testSecrets())
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("error = %v", err)
	}
}

func TestVersion2ProviderValidation(t *testing.T) {
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
			_, _, err := loadText(t, strings.Replace(validConfigV2(), tt.old, tt.replacement, 1), testSecrets())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v, want containing %q", err, tt.want)
			}
		})
	}
	t.Run("missing provider credential", func(t *testing.T) {
		env := testSecrets()
		delete(env, "DEEPSEEK_API_KEY")
		_, _, err := loadText(t, validConfigV2(), env)
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
		{"flash model", func(s string) string { return strings.Replace(s, "id: flash", "id: ''", 1) }, "models.flash.id"},
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
version: 1
server:
  listen: ':8080'
  public_base_url: https://eggy.example
  telegram_webhook_path: /webhooks/telegram
data_dir: /data
telegram:
  owner_id: 42
models:
  flash:
    adapter: deepseek
    id: flash
  pro:
    adapter: deepseek
    id: pro
  escalation:
    tool_steps: 4
    recoverable_failures: 2
repositories:
  - name: repo
    clone_url: https://github.com/acme/repo.git
    base_branch: main
    protected_branches: [main]
runner:
  root: /tmp/runs
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

func validConfigV2() string {
	body := strings.Replace(validConfig(), "version: 1", "version: 2", 1)
	legacyModels := `models:
  flash:
    adapter: deepseek
    id: flash
  pro:
    adapter: deepseek
    id: pro
  escalation:
    tool_steps: 4
    recoverable_failures: 2`
	v2Models := `agent:
  default_model: deepseek-pro
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
models:
  deepseek-pro:
    provider: deepseek
    model: deepseek-v4-pro`
	return strings.Replace(body, legacyModels, v2Models, 1)
}

func TestConfigRejectsUnsupportedModelAdapter(t *testing.T) {
	cfg := Config{Version: 1, Server: ServerConfig{PublicBaseURL: "https://eggy.test", TelegramWebhookPath: "/hook"}, Telegram: TelegramConfig{OwnerID: 1}, legacyModels: ModelsConfig{Flash: ModelConfig{Adapter: "unknown", ID: "flash"}, Pro: ModelConfig{Adapter: "deepseek", ID: "pro"}}, Runner: RunnerConfig{Timeout: Duration(time.Minute), Retention: Duration(time.Minute), MaxOutputBytes: 1}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error=%v", err)
	}
}
