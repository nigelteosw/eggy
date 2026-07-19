package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigAcceptsExample(t *testing.T) {
	env := testSecrets()
	cfg, secrets, err := LoadConfig(filepath.Join("..", "..", "config.example.yaml"), mapEnv(env))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Telegram.OwnerID != 123456789 || cfg.Models.Flash.ID != "deepseek-v4-flash" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.Runner.Timeout.Value() != 45*time.Minute || cfg.Server.Listen != ":8080" {
		t.Fatalf("defaults/durations not loaded: %#v", cfg)
	}
	if secrets.DeepSeekAPIKey != env["DEEPSEEK_API_KEY"] {
		t.Fatal("DeepSeek secret was not loaded")
	}
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

func TestConfigRejectsUnsupportedModelAdapter(t *testing.T) {
	cfg := Config{Version: 1, Server: ServerConfig{PublicBaseURL: "https://eggy.test", TelegramWebhookPath: "/hook"}, Telegram: TelegramConfig{OwnerID: 1}, Models: ModelsConfig{Flash: ModelConfig{Adapter: "unknown", ID: "flash"}, Pro: ModelConfig{Adapter: "deepseek", ID: "pro"}}, Runner: RunnerConfig{Timeout: Duration(time.Minute), Retention: Duration(time.Minute), MaxOutputBytes: 1}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error=%v", err)
	}
}
