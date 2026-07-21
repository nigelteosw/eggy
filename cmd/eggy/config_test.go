package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/bootstrap"
)

func writeTestConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	body := `version: 2
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
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeConfigCLI(t *testing.T, path string, args ...string) bootstrap.CommandResult {
	t.Helper()
	result, handled, err := bootstrap.ExecuteConfigCLI(context.Background(), path, append([]string{"config"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatalf("config %v was not handled", args)
	}
	return result
}

func TestConfigCLIGetSections(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "get", "path")
	if result.RenderPlainText() != path {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
}

func TestConfigCLISetProvider(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "set", "provider", "--name=openrouter", "--adapter=openai_compatible", "--base-url=https://openrouter.ai/api/v1", "--api-key-env=OPENROUTER_API_KEY")
	if !strings.Contains(result.RenderPlainText(), "Set provider openrouter.") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
	result = executeConfigCLI(t, path, "get", "providers")
	output := result.RenderPlainText()
	if !strings.Contains(output, "openrouter") || !strings.Contains(output, "openai_compatible") || !strings.Contains(output, "https://openrouter.ai/api/v1") || !strings.Contains(output, "OPENROUTER_API_KEY") {
		t.Fatalf("output=%q", output)
	}
}

func TestConfigCLISetModel(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "set", "model", "--alias=openrouter-pro", "--provider=deepseek", "--model=your-model-id")
	if !strings.Contains(result.RenderPlainText(), "Set model openrouter-pro.") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
	result = executeConfigCLI(t, path, "get", "models")
	output := result.RenderPlainText()
	if !strings.Contains(output, "openrouter-pro") || !strings.Contains(output, "deepseek") || !strings.Contains(output, "your-model-id") {
		t.Fatalf("output=%q", output)
	}
}

func TestConfigCLISetCalendarPatchSemantics(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "get", "calendar")
	if !strings.Contains(result.RenderPlainText(), "UTC") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
	result = executeConfigCLI(t, path, "set", "calendar", "--timezone=Asia/Singapore")
	if !strings.Contains(result.RenderPlainText(), "Set calendar.") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
	result = executeConfigCLI(t, path, "get", "calendar")
	if !strings.Contains(result.RenderPlainText(), "Asia/Singapore") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
}

func TestConfigCLIShow(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "show")
	output := result.RenderPlainText()
	if !strings.Contains(output, "version: 2") || !strings.Contains(output, "deepseek") {
		t.Fatalf("output=%q", output)
	}
}

func TestConfigCLIUnknownVerb(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	result := executeConfigCLI(t, path, "nonsense")
	if !strings.Contains(result.RenderPlainText(), "Usage:") {
		t.Fatalf("output=%q", result.RenderPlainText())
	}
}
