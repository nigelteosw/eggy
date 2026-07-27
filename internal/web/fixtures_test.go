package web

// validConfig is the config.yaml the web tests write when they need a
// file that survives config.LoadConfig and Validate. internal/config keeps its
// own copy for its own tests: the two packages exercise different things, and
// a shared fixture would mean either exporting a test helper or letting a
// change made for one package's tests silently alter the other's.
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
