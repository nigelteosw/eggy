package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadConfigResolvesWebUICredentialsAndRequiresEncryptionKeyWhenSet(t *testing.T) {
	body := validConfig()
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

// Config holds environment variable *names*; Secrets holds the values
// resolved from them. Marshaling a Config must therefore never emit a
// resolved secret -- that is what makes it safe to render config back to the
// owner (/config, the web config routes) without redaction.
//
// This assertion arrived with the web search config and outlived it: it was
// never really about web search, and the property still binds every provider
// key, MCP bearer token, and the GitHub token.
func TestMarshaledConfigNeverLeaksAResolvedSecret(t *testing.T) {
	env := testSecrets()
	cfg, secrets, err := loadText(t, validConfig(), env)
	if err != nil {
		t.Fatal(err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, resolved := range secrets.Values() {
		if strings.Contains(string(body), resolved) {
			t.Fatalf("marshaled config leaked a resolved secret:\n%s", body)
		}
	}
	// The environment names themselves must survive, or the round-trip would
	// silently drop the binding rather than the value.
	if !strings.Contains(string(body), "api_key_env: DEEPSEEK_API_KEY") {
		t.Fatalf("marshaled config dropped the environment binding:\n%s", body)
	}
}

// Values is the single list of live credentials, and both log redaction and
// the durable-context secret guard read it. A field added to Secrets but not to
// Values is therefore a credential that gets written to logs and to owner-facing
// context unmasked, which is exactly how the Google client secret and the MCP
// OAuth client secrets once escaped the guard. Reflection is used deliberately:
// the point is to fail when a *new* field is added, which a hand-written list
// cannot do.
func TestValuesCoversEverySecretField(t *testing.T) {
	var secrets Secrets
	value := reflect.ValueOf(&secrets).Elem()
	markers := map[string]string{}
	for i := range value.NumField() {
		name := value.Type().Field(i).Name
		marker := "marker-for-" + name
		markers[name] = marker
		switch field := value.Field(i); field.Kind() {
		case reflect.String:
			field.SetString(marker)
		case reflect.Map:
			field.Set(reflect.ValueOf(map[string]string{"only": marker}))
		default:
			t.Fatalf("Secrets.%s has unhandled kind %s; teach this test how to fill it", name, field.Kind())
		}
	}
	// UIUserEmail is an identity rather than a credential, so it is the one
	// field Values legitimately omits.
	delete(markers, "UIUserEmail")

	present := map[string]bool{}
	for _, resolved := range secrets.Values() {
		present[resolved] = true
	}
	for name, marker := range markers {
		if !present[marker] {
			t.Errorf("Secrets.%s is missing from Values(), so it is redacted nowhere", name)
		}
	}
}

// OpenAI is reachable without a new adapter: "openai_compatible" *is* OpenAI's
// Chat Completions wire format, so ChatGPT models are a providers entry rather
// than a plugin package. This pins that, because the alternative -- writing an
// "openai" adapter that duplicates openaicompat -- is the obvious wrong turn
// when adding it.
func TestOpenAIIsAProviderEntryNotANewAdapter(t *testing.T) {
	body := strings.Replace(validConfig(), `providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY`, `providers:
  openai:
    adapter: openai_compatible
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY`, 1)
	body = strings.Replace(body, "provider: deepseek", "provider: openai", 1)

	env := testSecrets()
	env["OPENAI_API_KEY"] = "sk-test"
	cfg, secrets, err := loadText(t, body, env)
	if err != nil {
		t.Fatal(err)
	}
	provider, model, err := cfg.ActiveModel("deepseek-pro")
	if err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != "https://api.openai.com/v1" || model.Provider != "openai" {
		t.Fatalf("provider=%#v model=%#v", provider, model)
	}
	if secrets.ProviderAPIKeys["openai"] != "sk-test" {
		t.Fatalf("api key not resolved: %#v", secrets.ProviderAPIKeys)
	}
}

// An unsupported adapter names what is supported, so adding a backend does not
// start with guessing the vocabulary.
func TestUnsupportedAdapterNamesTheSupportedOnes(t *testing.T) {
	broken := strings.Replace(validConfig(), "adapter: openai_compatible", "adapter: anthropic", 1)
	_, _, err := loadText(t, broken, testSecrets())
	if err == nil || !strings.Contains(err.Error(), "supported adapters are openai_compatible") {
		t.Fatalf("error=%v", err)
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
		{"transport", "streamable-http", "sse", "unsupported transport"},
		{"auth", "auth: oauth", "auth: token", "unsupported auth"},
		{"stdio fields on http", "auth: oauth", "auth: oauth\n      command: npx", "apply only to the stdio transport"},
		{"empty require_approval entry", "enabled: true", "enabled: true\n      require_approval: [\"\"]", "require_approval must not contain empty names"},
		{"duplicate require_approval entry", "enabled: true", "enabled: true\n      require_approval: [send, send]", "duplicate require_approval entry"},
		// A gate on a tool the filter already removed is a contradiction, and
		// the likely reading is that the gate the owner wanted is on a
		// different tool -- one that is still ungated.
		{"require_approval on an excluded tool", "enabled: true", "enabled: true\n      require_approval: [send]\n      tool_filter:\n        exclude: [send]", "requires approval for excluded tool"},
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

func TestLoadConfigAcceptsStdioMCP(t *testing.T) {
	cfg, _, err := loadText(t, validConfig()+`
mcp:
  servers:
    filesystem:
      transport: stdio
      auth: none
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/data/repos"]
      env_allowlist: [FILESYSTEM_ROOT]
      enabled: true
`, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	server := cfg.MCP.Servers["filesystem"]
	if server.Command != "npx" || len(server.Args) != 3 || len(server.EnvAllowlist) != 1 {
		t.Fatalf("stdio server = %#v", server)
	}
	if server.ConnectTimeout.Value() != 10*time.Second || server.MaxOutputBytes != 128<<10 {
		t.Fatalf("stdio server missed shared defaults: %#v", server)
	}
}

func TestStdioMCPValidation(t *testing.T) {
	base := validConfig() + `
mcp:
  servers:
    filesystem:
      transport: stdio
      auth: none
      command: npx
      enabled: true
`
	tests := []struct{ name, old, replacement, want string }{
		{"url", "command: npx", "command: npx\n      url: https://mcp.example.com", "url applies only to the streamable-http"},
		{"missing command", "command: npx", "command: \"\"", "must set a command"},
		{"auth", "auth: none", "auth: oauth", "must use auth none"},
		{"empty arg", "command: npx", "command: npx\n      args: [\"\"]", "args must not contain empty values"},
		{"env name", "command: npx", "command: npx\n      env_allowlist: [lowercase]", "env_allowlist entry"},
		{"duplicate env", "command: npx", "command: npx\n      env_allowlist: [HOME, HOME]", "duplicate env_allowlist"},
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
	body := validConfig() + `
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

// An absent heartbeat: section is the common case -- every config deployed
// before the heartbeat existed -- and must keep loading under
// KnownFields(true), leaving the capability off.
func TestLoadConfigWithoutHeartbeatSectionLeavesItOff(t *testing.T) {
	cfg, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if interval := cfg.Heartbeat.Interval.Value(); interval != 0 {
		t.Fatalf("heartbeat interval = %v, want 0 (off) when the section is absent", interval)
	}
}

func TestLoadConfigAcceptsHeartbeatInterval(t *testing.T) {
	cfg, _, err := loadText(t, validConfig()+"heartbeat:\n  interval: 30m\n", testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if interval := cfg.Heartbeat.Interval.Value(); interval != 30*time.Minute {
		t.Fatalf("heartbeat interval = %v, want 30m", interval)
	}
}

func TestLoadConfigRejectsNegativeHeartbeatInterval(t *testing.T) {
	if _, _, err := loadText(t, validConfig()+"heartbeat:\n  interval: -5m\n", testSecrets()); err == nil {
		t.Fatal("a negative heartbeat interval must be rejected")
	}
}

func TestLoadConfigRejectsRemovedFeatureSections(t *testing.T) {
	// heartbeat is deliberately absent: it came back as a real section (an
	// interval and an instruction), narrower than the deleted scheduler:
	// block, which stays rejected.
	for _, section := range []string{"embeddings", "implementation_sessions", "scheduler"} {
		t.Run(section, func(t *testing.T) {
			_, _, err := loadText(t, validConfig()+section+": {}\n", testSecrets())
			if err == nil || !strings.Contains(err.Error(), "field "+section) {
				t.Fatalf("removed section %q error=%v", section, err)
			}
		})
	}
}

// Native Calendar is gone -- a calendar server is configured under mcp like
// any other capability. A deployment still holding the section must fail
// loudly here rather than load with a silently ignored key; LoadOrCreateConfig
// prunes it as a retired field instead (see config_init_test.go).
func TestLoadConfigRejectsRetiredCalendarSection(t *testing.T) {
	if _, _, err := loadText(t, validConfig()+"calendar:\n  default_calendar: primary\n", testSecrets()); err == nil {
		t.Fatal("retired calendar section was accepted")
	}
}

func TestLoadConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		rewrite func(string) string
		want    string
	}{
		{"owner", func(s string) string { return strings.Replace(s, "owner_id: 42", "owner_id: 0", 1) }, "owner.id must be set"},
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

func TestLoadConfigRequiresSecretsForEnabledCapabilities(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN"},
		{"TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_WEBHOOK_SECRET"},
		{"DEEPSEEK_API_KEY", "DEEPSEEK_API_KEY"},
		{"GITHUB_TOKEN", "GITHUB_TOKEN"},
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
  timezone: UTC
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
`
}

// webOnlyConfig drops the telegram block entirely and sets owner.id
// directly -- the deployment shape the canonical owner identity exists for.
func webOnlyConfig() string {
	return strings.Replace(validConfig(), "telegram:\n  owner_id: 42\n", "owner:\n  id: owner-42\n", 1)
}

// webOnlySecrets omits TELEGRAM_BOT_TOKEN and TELEGRAM_WEBHOOK_SECRET.
func webOnlySecrets() map[string]string {
	env := testSecrets()
	delete(env, "TELEGRAM_BOT_TOKEN")
	delete(env, "TELEGRAM_WEBHOOK_SECRET")
	return env
}

func TestWebOnlyDeploymentBootsWithoutAnyTelegramConfiguration(t *testing.T) {
	config, _, err := loadText(t, webOnlyConfig(), webOnlySecrets())
	if err != nil {
		t.Fatalf("a web-only deployment must not need Telegram configured: %v", err)
	}
	if config.Owner.ID != "owner-42" {
		t.Fatalf("owner.id=%q", config.Owner.ID)
	}
	if config.Telegram.Configured() {
		t.Fatalf("telegram=%#v, want unconfigured", config.Telegram)
	}
}

func TestTelegramCredentialsAreRequiredOnlyWhenTelegramIsConfigured(t *testing.T) {
	if _, _, err := loadText(t, validConfig(), webOnlySecrets()); err == nil ||
		!strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatalf("a Telegram deployment still requires its credentials, got %v", err)
	}
	if _, _, err := loadText(t, webOnlyConfig(), webOnlySecrets()); err != nil {
		t.Fatalf("a web-only deployment must not require Telegram credentials: %v", err)
	}
}

func TestOwnerIDStillDerivesFromAndMustMatchTelegramOwnerID(t *testing.T) {
	// An existing config carrying only telegram.owner_id keeps working.
	config, _, err := loadText(t, validConfig(), testSecrets())
	if err != nil || config.Owner.ID != "42" {
		t.Fatalf("owner.id=%q err=%v", config.Owner.ID, err)
	}
	// Setting both to conflicting values is a configuration mistake, not a
	// web-only deployment.
	conflicting := strings.Replace(validConfig(), "telegram:\n", "owner:\n  id: someone-else\ntelegram:\n", 1)
	if _, _, err := loadText(t, conflicting, testSecrets()); err == nil ||
		!strings.Contains(err.Error(), "owner.id must match telegram.owner_id") {
		t.Fatalf("error=%v", err)
	}
}

func TestNegativeTelegramOwnerIDIsRejectedAsATypo(t *testing.T) {
	negative := strings.Replace(validConfig(), "owner_id: 42", "owner_id: -42", 1)
	if _, _, err := loadText(t, negative, testSecrets()); err == nil ||
		!strings.Contains(err.Error(), "telegram.owner_id must be positive when set") {
		t.Fatalf("error=%v", err)
	}
}

// A section that decodes but never reaches Config is worse than one that fails
// to parse: startup succeeds, the capability is silently absent, and the next
// config write from any surface erases what the owner wrote. Google is carried
// through load and back out through marshal.
func TestGoogleSectionSurvivesLoadAndWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
google:
  enabled: true
  client_id: "x.apps.googleusercontent.com"
  client_secret_env: "GOOGLE_CLIENT_SECRET"
  products: ["calendar", "gmail"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	env := testSecrets()
	env["GOOGLE_CLIENT_SECRET"] = "secret"
	cfg, _, err := LoadConfig(path, mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Google.Enabled || cfg.Google.ClientID != "x.apps.googleusercontent.com" || len(cfg.Google.Products) != 2 {
		t.Fatalf("google=%#v", cfg.Google)
	}

	marshalled, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marshalled), "x.apps.googleusercontent.com") {
		t.Fatalf("google section dropped on write:\n%s", marshalled)
	}
}

// Validation accepts a product name in any casing, so load must canonicalize
// it rather than leave every downstream reader to lowercase it again. The two
// readers disagreeing is not hypothetical: the adapter matched case-insensitively
// and registered the tool, while scope selection matched exactly and requested
// nothing, so a hand-written "Gmail" produced a tool that 403s on every call.
func TestGoogleProductsAreCanonicalAfterLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
google:
  enabled: true
  client_id: "x.apps.googleusercontent.com"
  products: ["Gmail", " Calendar "]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gmail", "calendar"}
	if !slices.Equal(cfg.Google.Products, want) {
		t.Fatalf("products=%q, want %q", cfg.Google.Products, want)
	}
}

// An enabled Google needs its client secret present, for the same reason a
// bearer-env MCP server needs its token: a missing variable otherwise fails at
// the first call rather than at boot.
func TestEnabledGoogleRequiresItsSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := validConfig() + `
google:
  enabled: true
  client_id: "x.apps.googleusercontent.com"
  client_secret_env: "GOOGLE_CLIENT_SECRET"
  products: ["calendar"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("error=%v", err)
	}
}

func TestActiveHoursWindow(t *testing.T) {
	utc := time.UTC
	at := func(hour, minute int) time.Time { return time.Date(2026, 8, 24, hour, minute, 0, 0, utc) }

	for name, tt := range map[string]struct {
		hours ActiveHours
		when  time.Time
		want  bool
	}{
		"unset is always active":       {hours: ActiveHours{}, when: at(3, 0), want: true},
		"partial is always active":     {hours: ActiveHours{Start: "08:00"}, when: at(3, 0), want: true},
		"inside the window":            {hours: ActiveHours{Start: "08:00", End: "22:00"}, when: at(12, 0), want: true},
		"start is inclusive":           {hours: ActiveHours{Start: "08:00", End: "22:00"}, when: at(8, 0), want: true},
		"end is exclusive":             {hours: ActiveHours{Start: "08:00", End: "22:00"}, when: at(22, 0), want: false},
		"before the window":            {hours: ActiveHours{Start: "08:00", End: "22:00"}, when: at(3, 0), want: false},
		"after the window":             {hours: ActiveHours{Start: "08:00", End: "22:00"}, when: at(23, 30), want: false},
		"midnight to 24:00 is all day": {hours: ActiveHours{Start: "00:00", End: "24:00"}, when: at(3, 0), want: true},
		// A window whose end is before its start wraps midnight, which is the
		// natural way to write an overnight watch.
		"wrapped window covers the evening": {hours: ActiveHours{Start: "22:00", End: "06:00"}, when: at(23, 0), want: true},
		"wrapped window covers the morning": {hours: ActiveHours{Start: "22:00", End: "06:00"}, when: at(2, 0), want: true},
		"wrapped window excludes midday":    {hours: ActiveHours{Start: "22:00", End: "06:00"}, when: at(12, 0), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tt.hours.Active(tt.when); got != tt.want {
				t.Fatalf("Active(%s)=%v want %v", tt.when.Format("15:04"), got, tt.want)
			}
		})
	}
}

// A quiet-hours window that does not work is indistinguishable from a broken
// heartbeat, so a malformed one is rejected at load rather than ignored.
func TestActiveHoursValidation(t *testing.T) {
	for name, tt := range map[string]struct {
		hours   ActiveHours
		wantErr bool
	}{
		"unset":               {hours: ActiveHours{}},
		"valid":               {hours: ActiveHours{Start: "08:00", End: "22:00"}},
		"end 24:00":           {hours: ActiveHours{Start: "08:00", End: "24:00"}},
		"wrapped":             {hours: ActiveHours{Start: "22:00", End: "06:00"}},
		"only start":          {hours: ActiveHours{Start: "08:00"}, wantErr: true},
		"only end":            {hours: ActiveHours{End: "22:00"}, wantErr: true},
		"unparseable":         {hours: ActiveHours{Start: "8am", End: "22:00"}, wantErr: true},
		"hour out of range":   {hours: ActiveHours{Start: "25:00", End: "22:00"}, wantErr: true},
		"start 24:00":         {hours: ActiveHours{Start: "24:00", End: "22:00"}, wantErr: true},
		"minute out of range": {hours: ActiveHours{Start: "08:60", End: "22:00"}, wantErr: true},
		// Equal bounds are ambiguous -- zero-length or all-day -- so they are
		// refused rather than guessed at.
		"equal bounds": {hours: ActiveHours{Start: "08:00", End: "08:00"}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := tt.hours.Validate()
			if tt.wantErr != (err != nil) {
				t.Fatalf("Validate()=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// A malformed window fails the whole config load, so a heartbeat never
// silently stops beating because of a typo in a clock time.
func TestConfigRejectsAMalformedActiveHoursWindow(t *testing.T) {
	body := validConfig() + "heartbeat:\n  interval: 3h\n  active_hours:\n    start: 8am\n    end: \"22:00\"\n"
	if _, _, err := loadText(t, body, testSecrets()); err == nil {
		t.Fatal("a malformed active_hours window loaded")
	}
}

// The whole section is optional, and both new keys are optional within it, so
// a config written before this existed still loads under KnownFields(true).
func TestConfigLoadsAnActiveHoursWindow(t *testing.T) {
	body := validConfig() + "heartbeat:\n  interval: 3h\n  include_recent_history: true\n  active_hours:\n    start: \"08:00\"\n    end: \"22:00\"\n"
	cfg, _, err := loadText(t, body, testSecrets())
	if err != nil {
		t.Fatalf("loadText: %v", err)
	}
	if !cfg.Heartbeat.IncludeRecentHistory {
		t.Fatal("include_recent_history did not round-trip")
	}
	if got := cfg.Heartbeat.ActiveHours; got.Start != "08:00" || got.End != "22:00" {
		t.Fatalf("active_hours=%+v", got)
	}
}
