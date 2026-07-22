package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/bootstrap"
)

func TestMCPCLILoadsOnlyMCPSecretsAndRuntime(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	body := strings.Replace(writeTestConfigBody(), "data_dir: /data", "data_dir: "+directory, 1) + `
mcp:
  servers:
    example:
      url: https://mcp.example.com
      transport: streamable-http
      auth: none
      enabled: true
      tool_filter:
        include: [read]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, secrets, err := bootstrap.LoadMCPConfig(path, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	result, handled, err := bootstrap.ExecuteMCPCLI(context.Background(), config, secrets, bootstrap.AppOptions{FakeAdapters: true}, []string{"mcp"})
	if err != nil || !handled || !strings.Contains(result.RenderPlainText(), "example") {
		t.Fatalf("output=%q handled=%v err=%v", result.RenderPlainText(), handled, err)
	}
}

func writeTestConfigBody() string {
	return `server:
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
scheduler:
  heartbeat_cadence: 30m
  quiet_hours: {start: '22:00', end: '07:00', timezone: UTC}
  minimum_proactive_interval: 2h
  weekly_proactive_limit: 3
calendar:
  enabled: false
  default_calendar: primary
  timezone: UTC
`
}
