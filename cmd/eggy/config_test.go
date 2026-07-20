package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigMainGetSections(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"get", "path"})
	if err != nil || output != path {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainSetProvider(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"set", "provider", "--name=openrouter", "--adapter=openai_compatible", "--base-url=https://openrouter.ai/api/v1", "--api-key-env=OPENROUTER_API_KEY"})
	if err != nil || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"get", "providers"})
	if err != nil || !strings.Contains(output, "openrouter  adapter=openai_compatible  base_url=https://openrouter.ai/api/v1  api_key_env=OPENROUTER_API_KEY") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainSetModel(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"set", "model", "--alias=openrouter-pro", "--provider=deepseek", "--model=your-model-id"})
	if err != nil || output != "Set model openrouter-pro. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"get", "models"})
	if err != nil || !strings.Contains(output, "openrouter-pro  provider=deepseek  model=your-model-id") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainSetCalendarPatchSemantics(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"get", "calendar"})
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=UTC" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"set", "calendar", "--timezone=Asia/Singapore"})
	if err != nil || output != "Set calendar. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"get", "calendar"})
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=Asia/Singapore" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainShow(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"show"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "version: 2") || !strings.Contains(output, "deepseek") {
		t.Fatalf("output=%q", output)
	}
}

func TestConfigMainUnknownVerb(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	_, err := configMain(path, []string{"nonsense"})
	if err == nil || !strings.Contains(err.Error(), "usage: eggy config") {
		t.Fatalf("err=%v", err)
	}
}
