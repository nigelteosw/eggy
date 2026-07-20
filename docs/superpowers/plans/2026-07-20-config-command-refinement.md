# Config Command Refinement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite Telegram's `/config set` from positional arguments to `key=value` flags, add `calendar` as a fourth settable section, and give the `eggy` CLI its own separate `config get`/`set`/`show` implementation with real `--flag=value` syntax — no longer routed through the Telegram-shared dispatcher.

**Architecture:** `internal/bootstrap/config_mutate.go` becomes the sole, exported, interface-agnostic business-logic layer (validation, atomic writes, concurrency safety — unchanged in substance from the previous round, just exported and extended with calendar/show support). `CommandService.Execute` (Telegram) and a new `cmd/eggy/config.go` (CLI) become two independent thin adapters over that same layer, each with its own argument syntax suited to its interface.

**Tech Stack:** Go 1.26 standard library (`flag`, `strconv`, `strings.Cut`), `gopkg.in/yaml.v3`, the existing `internal/adapters/filelock` package.

## Global Constraints

- `config_mutate.go` remains the single source of truth for validation (`Config.Validate()`), atomic writes (temp file + `os.Rename`), and concurrency safety (`filelock.With` covering the full load-mutate-write sequence) — neither interface layer duplicates or can weaken these guarantees.
- `/config set` (both interfaces) only ever writes `coding.agents.*`, `providers.*`, `models.*`, `calendar.*`. Never `telegram.owner_id`, `server.*`, `runner.*`, or any other field.
- `/config set` requires a Version 2 config file, unchanged from the previous round.
- Never print a secret value through any `get`/`set`/`show` output — only adapter names, URLs, and environment-variable *names* ever appear.
- `eggy config show` is CLI-only. There is no Telegram equivalent, and `CommandService.Execute` must have no code path that can reach it.
- `eggy config ...` (all verbs) never constructs a `bootstrap.App` — it operates on the file at `-config`/`EGGY_CONFIG` directly, unlike every other CLI command.
- Write each behavior test-first and run `make fmt vet test race build` before completion.

---

### Task 1: Export the business-logic layer, add calendar and show, rewrite Telegram's `/config`

**Files:**
- Modify: `internal/bootstrap/config_mutate.go`
- Modify: `internal/bootstrap/config_mutate_test.go`
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`

**Interfaces:**
- Consumes: `Config`, `CalendarConfig`, `filelock.With`, `Config.Validate()`, `Config.MarshalYAML()` — all already in package `bootstrap`.
- Produces (all exported, callable from `cmd/eggy` in Task 2): `SetCodingAgent(path, alias, adapter, credentialEnv string) error`, `SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error`, `SetModelAlias(path, alias, provider, modelID string) error`, `SetCalendar(path, enabled, defaultCalendar, timezone string) error` (each of the three inputs empty means "leave that field unchanged" — patch semantics), `GetCodingConfigText(path string) (string, error)`, `GetProvidersConfigText(path string) (string, error)`, `GetModelAliasesConfigText(path string) (string, error)`, `GetCalendarConfigText(path string) (string, error)`, `ShowConfigText(path string) (string, error)`.

- [ ] **Step 1: Write failing tests**

Replace `internal/bootstrap/config_mutate_test.go` in full:

```go
package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSetCodingAgentAddsAndOverwritesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetCodingAgent(path, "claude", "claude_cli", "CLAUDE_CODE_OAUTH_TOKEN"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatalf("reload after set: %v", err)
	}
	agent, ok := reloaded.Coding.Agents["claude"]
	if !ok || agent.Adapter != "claude_cli" || agent.CredentialEnv != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("claude agent = %#v, ok=%v", agent, ok)
	}
	if _, ok := reloaded.Coding.Agents["codex"]; !ok {
		t.Fatal("existing codex agent was dropped")
	}
	if reloaded.Coding.DefaultAgent != "codex" {
		t.Fatalf("default_agent = %q, want codex unchanged", reloaded.Coding.DefaultAgent)
	}

	if err := SetCodingAgent(path, "claude", "claude_cli", "OTHER_TOKEN_ENV"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err = LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Coding.Agents["claude"].CredentialEnv != "OTHER_TOKEN_ENV" {
		t.Fatalf("overwrite did not take effect: %#v", reloaded.Coding.Agents["claude"])
	}
}

func TestSetCodingAgentRejectsInvalidAdapterAndLeavesFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfigV2())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetCodingAgent(path, "claude", "not_a_real_adapter", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported coding agent adapter") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetCodingAgentRejectsVersion1Config(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	before := []byte(validConfig())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetCodingAgent(path, "claude", "claude_cli", "CLAUDE_CODE_OAUTH_TOKEN")
	if err == nil || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetProviderAddsEntryAndRejectsInvalidURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetModelAlias(path, "deepseek-fast", "deepseek", "deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	model, ok := reloaded.ModelAliases["deepseek-fast"]
	if !ok || model.Provider != "deepseek" || model.Model != "deepseek-v4-flash" {
		t.Fatalf("deepseek-fast model = %#v, ok=%v", model, ok)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = SetModelAlias(path, "orphan", "does-not-exist", "some-model")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestSetCalendarPatchesOnlyGivenFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
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
	before := []byte(validConfigV2())
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
	before := []byte(validConfigV2())
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetCalendar(path, "not-a-bool", "", "")
	if err == nil || !strings.Contains(err.Error(), "enabled must be true or false") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestGetConfigTextFormatsEachSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	coding, err := GetCodingConfigText(path)
	if err != nil || coding != "default_agent: codex\ncodex  adapter=codex_cli" {
		t.Fatalf("coding text = %q, err=%v", coding, err)
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
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := ShowConfigText(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: 2", "deepseek", "public_base_url", "calendar"} {
		if !strings.Contains(text, want) {
			t.Fatalf("show text missing %q: %s", want, text)
		}
	}
}

func TestSetCodingAgentSerializesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	aliases := []string{"alpha", "beta", "gamma", "delta"}
	start := make(chan struct{})
	errorsChannel := make(chan error, len(aliases))
	var workers sync.WaitGroup
	for _, alias := range aliases {
		workers.Add(1)
		go func(alias string) {
			defer workers.Done()
			<-start
			errorsChannel <- SetCodingAgent(path, alias, "codex_cli", "")
		}(alias)
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent set error = %v", err)
		}
	}
	reloaded, _, err := LoadConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatalf("final config did not reload: %v", err)
	}
	for _, alias := range aliases {
		if _, ok := reloaded.Coding.Agents[alias]; !ok {
			t.Fatalf("alias %q missing after concurrent writes: %#v", alias, reloaded.Coding.Agents)
		}
	}
}
```

Replace the two Telegram-facing tests in `internal/bootstrap/commands_test.go` (`TestCommandConfigGetAndSetRoundTrip` and `TestCommandConfigUsageErrors`) — leave `TestCommandConfigReportsUnconfigured` and everything else in the file untouched:

```go
func TestCommandConfigGetAndSetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{configPath: path}
	ctx := context.Background()

	output, handled, err := commands.Execute(ctx, "/config get coding")
	if err != nil || !handled || output != "default_agent: codex\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}

	output, _, err = commands.Execute(ctx, "/config set coding_agent alias=claude adapter=claude_cli credential_env=CLAUDE_CODE_OAUTH_TOKEN")
	if err != nil || output != "Set coding agent claude. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get coding")
	if err != nil || output != "default_agent: codex\nclaude  adapter=claude_cli  credential_env=CLAUDE_CODE_OAUTH_TOKEN\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set coding_agent alias=claude adapter=bad_adapter")
	if err != nil || !strings.Contains(output, "unsupported coding agent adapter") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY")
	if err != nil || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get providers")
	if err != nil || !strings.Contains(output, "openrouter  adapter=openai_compatible  base_url=https://openrouter.ai/api/v1  api_key_env=OPENROUTER_API_KEY") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=openrouter-pro provider=openrouter model=your-model-id")
	if err != nil || output != "Set model openrouter-pro. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get models")
	if err != nil || !strings.Contains(output, "openrouter-pro  provider=openrouter  model=your-model-id") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model alias=orphan provider=missing-provider model=some-model")
	if err != nil || !strings.Contains(output, "unknown provider") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=UTC" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set calendar timezone=Asia/Singapore")
	if err != nil || output != "Set calendar. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get calendar")
	if err != nil || output != "enabled=true  default_calendar=primary  timezone=Asia/Singapore" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get path")
	if err != nil || output != path {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCommandConfigUsageErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := &CommandService{configPath: path}
	ctx := context.Background()
	tests := []struct{ input, want string }{
		{"/config", "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ..."},
		{"/config get", "Usage: /config get <coding|providers|models|calendar|path>"},
		{"/config get unknown", "Usage: /config get <coding|providers|models|calendar|path>"},
		{"/config set", "Usage: /config set <coding_agent|provider|model|calendar> ..."},
		{"/config set coding_agent alias=claude", "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"},
		{"/config set coding_agent alias=claude adapter=claude_cli unknown=x", "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"},
		{"/config set coding_agent notkeyvalue", `invalid flag "notkeyvalue": expected key=value`},
		{"/config set provider name=openrouter adapter=openai_compatible", "Usage: /config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"},
		{"/config set model alias=openrouter-pro provider=openrouter", "Usage: /config set model alias=<alias> provider=<provider> model=<model_id>"},
		{"/config set calendar badkey=x", "Usage: /config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]"},
		{"/config set calendar", "at least one of enabled, default_calendar, or timezone is required"},
	}
	for _, tt := range tests {
		output, handled, err := commands.Execute(ctx, tt.input)
		if err != nil || !handled || output != tt.want {
			t.Fatalf("input=%q output=%q handled=%v err=%v", tt.input, output, handled, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bootstrap -run 'SetCodingAgent|SetProvider|SetModelAlias|SetCalendar|GetConfigText|ShowConfigText|CommandConfig' -count=1`

Expected: build fails — `SetCodingAgent`, `SetCalendar`, `ShowConfigText`, etc. don't exist as exported names yet, and `commands.go` still calls the old lowercase names with old positional parsing.

- [ ] **Step 3: Implement**

Replace `internal/bootstrap/config_mutate.go` in full:

```go
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"gopkg.in/yaml.v3"
)

var errConfigSetRequiresVersion2 = errors.New("config.yaml is version 1; migrate to version 2 before using /config set")

func loadConfigDocument(path string) (Config, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, 0, fmt.Errorf("open config: %w", err)
	}
	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return Config{}, 0, fmt.Errorf("decode config: %w", err)
	}
	switch header.Version {
	case 1:
		var document legacyConfigDocument
		if err := decodeKnownYAML(data, &document); err != nil {
			return Config{}, 0, fmt.Errorf("decode config: %w", err)
		}
		return normalizeLegacyConfig(document), 1, nil
	case 2:
		var document configV2Document
		if err := decodeKnownYAML(data, &document); err != nil {
			return Config{}, 0, fmt.Errorf("decode config: %w", err)
		}
		return normalizeV2Config(document), 2, nil
	default:
		return Config{}, 0, errors.New("version must be 1 or 2")
	}
}

// writeConfigUnlocked persists cfg atomically. Callers must hold the path's
// filelock for the whole load-mutate-write sequence, not just this step, or
// concurrent writers can race: both read the old file, both mutate their own
// copy, and the second write silently discards the first writer's change.
func writeConfigUnlocked(path string, cfg Config) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	return os.Rename(temporaryPath, path)
}

func SetCodingAgent(path, alias, adapter, credentialEnv string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if cfg.Coding.Agents == nil {
			cfg.Coding.Agents = map[string]CodingAgentConfig{}
		}
		cfg.Coding.Agents[alias] = CodingAgentConfig{Adapter: adapter, CredentialEnv: credentialEnv}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		cfg.Providers[name] = ProviderConfig{Adapter: adapter, BaseURL: baseURL, APIKeyEnv: apiKeyEnv}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func SetModelAlias(path, alias, provider, modelID string) error {
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if cfg.ModelAliases == nil {
			cfg.ModelAliases = map[string]ModelAliasConfig{}
		}
		cfg.ModelAliases[alias] = ModelAliasConfig{Provider: provider, Model: modelID}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetCalendar patches CalendarConfig field-by-field: an empty string for
// enabled, defaultCalendar, or timezone means "leave that field unchanged."
// At least one must be non-empty.
func SetCalendar(path, enabled, defaultCalendar, timezone string) error {
	if enabled == "" && defaultCalendar == "" && timezone == "" {
		return errors.New("at least one of enabled, default_calendar, or timezone is required")
	}
	return filelock.With(path, func() error {
		cfg, version, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if version != 2 {
			return errConfigSetRequiresVersion2
		}
		if enabled != "" {
			parsed, err := strconv.ParseBool(enabled)
			if err != nil {
				return fmt.Errorf("enabled must be true or false: %w", err)
			}
			cfg.Calendar.Enabled = parsed
		}
		if defaultCalendar != "" {
			cfg.Calendar.DefaultCalendar = defaultCalendar
		}
		if timezone != "" {
			cfg.Calendar.Timezone = timezone
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func GetCodingConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.Coding.Agents))
	for alias := range cfg.Coding.Agents {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	lines := make([]string, 0, len(aliases)+1)
	lines = append(lines, "default_agent: "+cfg.Coding.DefaultAgent)
	for _, alias := range aliases {
		agent := cfg.Coding.Agents[alias]
		line := alias + "  adapter=" + agent.Adapter
		if agent.CredentialEnv != "" {
			line += "  credential_env=" + agent.CredentialEnv
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No providers configured.", nil
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		lines = append(lines, fmt.Sprintf("%s  adapter=%s  base_url=%s  api_key_env=%s", name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv))
	}
	return strings.Join(lines, "\n"), nil
}

func GetModelAliasesConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		return "No models configured.", nil
	}
	lines := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		model := cfg.ModelAliases[alias]
		lines = append(lines, fmt.Sprintf("%s  provider=%s  model=%s", alias, model.Provider, model.Model))
	}
	return strings.Join(lines, "\n"), nil
}

func GetCalendarConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("enabled=%t  default_calendar=%s  timezone=%s", cfg.Calendar.Enabled, cfg.Calendar.DefaultCalendar, cfg.Calendar.Timezone), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, _, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
```

In `internal/bootstrap/commands.go`, replace the entire `case "/config":` block (from `case "/config":` through its matching closing brace, immediately before `case "/usage":`) with:

```go
	case "/config":
		if s.configPath == "" {
			return "Config file management is not configured.", true, nil
		}
		if len(fields) < 2 {
			return "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ...", true, nil
		}
		switch fields[1] {
		case "get":
			if len(fields) != 3 {
				return "Usage: /config get <coding|providers|models|calendar|path>", true, nil
			}
			switch fields[2] {
			case "coding":
				text, err := GetCodingConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "providers":
				text, err := GetProvidersConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "models":
				text, err := GetModelAliasesConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "calendar":
				text, err := GetCalendarConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "path":
				return s.configPath, true, nil
			default:
				return "Usage: /config get <coding|providers|models|calendar|path>", true, nil
			}
		case "set":
			if len(fields) < 3 {
				return "Usage: /config set <coding_agent|provider|model|calendar> ...", true, nil
			}
			switch fields[2] {
			case "coding_agent":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]"
				for key := range values {
					if key != "alias" && key != "adapter" && key != "credential_env" {
						return usage, true, nil
					}
				}
				alias, adapter := values["alias"], values["adapter"]
				if alias == "" || adapter == "" {
					return usage, true, nil
				}
				if err := SetCodingAgent(s.configPath, alias, adapter, values["credential_env"]); err != nil {
					return err.Error(), true, nil
				}
				return "Set coding agent " + alias + ". Restart Eggy for this to take effect.", true, nil
			case "provider":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"
				for key := range values {
					if key != "name" && key != "adapter" && key != "base_url" && key != "api_key_env" {
						return usage, true, nil
					}
				}
				name, adapter, baseURL, apiKeyEnv := values["name"], values["adapter"], values["base_url"], values["api_key_env"]
				if name == "" || adapter == "" || baseURL == "" || apiKeyEnv == "" {
					return usage, true, nil
				}
				if err := SetProvider(s.configPath, name, adapter, baseURL, apiKeyEnv); err != nil {
					return err.Error(), true, nil
				}
				return "Set provider " + name + ". Restart Eggy for this to take effect.", true, nil
			case "model":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				usage := "Usage: /config set model alias=<alias> provider=<provider> model=<model_id>"
				for key := range values {
					if key != "alias" && key != "provider" && key != "model" {
						return usage, true, nil
					}
				}
				alias, provider, modelID := values["alias"], values["provider"], values["model"]
				if alias == "" || provider == "" || modelID == "" {
					return usage, true, nil
				}
				if err := SetModelAlias(s.configPath, alias, provider, modelID); err != nil {
					return err.Error(), true, nil
				}
				return "Set model " + alias + ". Restart Eggy for this to take effect.", true, nil
			case "calendar":
				values, err := parseConfigFlags(fields[3:])
				if err != nil {
					return err.Error(), true, nil
				}
				for key := range values {
					if key != "enabled" && key != "default_calendar" && key != "timezone" {
						return "Usage: /config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]", true, nil
					}
				}
				if err := SetCalendar(s.configPath, values["enabled"], values["default_calendar"], values["timezone"]); err != nil {
					return err.Error(), true, nil
				}
				return "Set calendar. Restart Eggy for this to take effect.", true, nil
			default:
				return "Usage: /config set <coding_agent|provider|model|calendar> ...", true, nil
			}
		default:
			return "Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ...", true, nil
		}
```

Add this helper function to `internal/bootstrap/commands.go`, after the `Execute` method:

```go
// parseConfigFlags parses "key=value" tokens into a map, splitting each on
// the first "=" only so a value containing "=" (a base_url query string,
// for instance) still parses correctly.
func parseConfigFlags(tokens []string) (map[string]string, error) {
	values := make(map[string]string, len(tokens))
	for _, token := range tokens {
		key, value, ok := strings.Cut(token, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid flag %q: expected key=value", token)
		}
		values[key] = value
	}
	return values, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bootstrap -run 'SetCodingAgent|SetProvider|SetModelAlias|SetCalendar|GetConfigText|ShowConfigText|CommandConfig' -count=1 -race`

Expected: PASS.

- [ ] **Step 5: Run the full package suite**

Run: `go test ./internal/bootstrap -count=1 -race`

Expected: PASS — confirms nothing else in the package broke from the rename.

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap/config_mutate.go internal/bootstrap/config_mutate_test.go internal/bootstrap/commands.go internal/bootstrap/commands_test.go
git commit -m "feat: export config mutation layer, add calendar section, key=value Telegram syntax"
```

### Task 2: CLI `config` implementation

**Files:**
- Create: `cmd/eggy/config.go`
- Create: `cmd/eggy/config_test.go`

**Interfaces:**
- Consumes: `bootstrap.SetCodingAgent`, `bootstrap.SetProvider`, `bootstrap.SetModelAlias`, `bootstrap.SetCalendar`, `bootstrap.GetCodingConfigText`, `bootstrap.GetProvidersConfigText`, `bootstrap.GetModelAliasesConfigText`, `bootstrap.GetCalendarConfigText`, `bootstrap.ShowConfigText` from Task 1.
- Produces: `configMain(configPath string, arguments []string) (string, error)`, called by `cmd/eggy/main.go` in Task 3.

- [ ] **Step 1: Write failing tests**

Create `cmd/eggy/config_test.go`:

```go
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
	output, err := configMain(path, []string{"get", "coding"})
	if err != nil || output != "default_agent: codex\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"get", "path"})
	if err != nil || output != path {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainSetCodingAgent(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	output, err := configMain(path, []string{"set", "coding-agent", "--alias=claude", "--adapter=claude_cli", "--credential-env=CLAUDE_CODE_OAUTH_TOKEN"})
	if err != nil || output != "Set coding agent claude. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = configMain(path, []string{"get", "coding"})
	if err != nil || output != "default_agent: codex\nclaude  adapter=claude_cli  credential_env=CLAUDE_CODE_OAUTH_TOKEN\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestConfigMainSetCodingAgentMissingRequiredFlag(t *testing.T) {
	path := writeTestConfig(t, t.TempDir())
	_, err := configMain(path, []string{"set", "coding-agent", "--alias=claude"})
	if err == nil || !strings.Contains(err.Error(), "usage: eggy config set coding-agent") {
		t.Fatalf("err=%v", err)
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/eggy -count=1`

Expected: compilation fails — `configMain` does not exist.

- [ ] **Step 3: Implement `cmd/eggy/config.go`**

```go
package main

import (
	"flag"
	"fmt"

	"github.com/nigelteosw/eggy/internal/bootstrap"
)

func configMain(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config get <coding|providers|models|calendar|path>|show|set <coding-agent|provider|model|calendar> ..."
	if len(arguments) == 0 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "get":
		return configGet(configPath, arguments[1:])
	case "set":
		return configSet(configPath, arguments[1:])
	case "show":
		return bootstrap.ShowConfigText(configPath)
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configGet(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config get <coding|providers|models|calendar|path>"
	if len(arguments) != 1 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "coding":
		return bootstrap.GetCodingConfigText(configPath)
	case "providers":
		return bootstrap.GetProvidersConfigText(configPath)
	case "models":
		return bootstrap.GetModelAliasesConfigText(configPath)
	case "calendar":
		return bootstrap.GetCalendarConfigText(configPath)
	case "path":
		return configPath, nil
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configSet(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config set <coding-agent|provider|model|calendar> ..."
	if len(arguments) == 0 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "coding-agent":
		return configSetCodingAgent(configPath, arguments[1:])
	case "provider":
		return configSetProvider(configPath, arguments[1:])
	case "model":
		return configSetModel(configPath, arguments[1:])
	case "calendar":
		return configSetCalendar(configPath, arguments[1:])
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configSetCodingAgent(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set coding-agent", flag.ContinueOnError)
	alias := flags.String("alias", "", "coding agent alias")
	adapter := flags.String("adapter", "", "adapter: codex_cli or claude_cli")
	credentialEnv := flags.String("credential-env", "", "environment variable name holding the credential (optional)")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *alias == "" || *adapter == "" {
		return "", fmt.Errorf("usage: eggy config set coding-agent --alias=<alias> --adapter=<codex_cli|claude_cli> [--credential-env=<ENV_NAME>]")
	}
	if err := bootstrap.SetCodingAgent(configPath, *alias, *adapter, *credentialEnv); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set coding agent %s. Restart Eggy for this to take effect.", *alias), nil
}

func configSetProvider(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set provider", flag.ContinueOnError)
	name := flags.String("name", "", "provider name")
	adapter := flags.String("adapter", "", "adapter: openai_compatible")
	baseURL := flags.String("base-url", "", "provider base URL")
	apiKeyEnv := flags.String("api-key-env", "", "environment variable name holding the API key")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *name == "" || *adapter == "" || *baseURL == "" || *apiKeyEnv == "" {
		return "", fmt.Errorf("usage: eggy config set provider --name=<name> --adapter=openai_compatible --base-url=<url> --api-key-env=<ENV_NAME>")
	}
	if err := bootstrap.SetProvider(configPath, *name, *adapter, *baseURL, *apiKeyEnv); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set provider %s. Restart Eggy for this to take effect.", *name), nil
}

func configSetModel(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set model", flag.ContinueOnError)
	alias := flags.String("alias", "", "model alias")
	provider := flags.String("provider", "", "provider name")
	model := flags.String("model", "", "model ID")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *alias == "" || *provider == "" || *model == "" {
		return "", fmt.Errorf("usage: eggy config set model --alias=<alias> --provider=<provider> --model=<model_id>")
	}
	if err := bootstrap.SetModelAlias(configPath, *alias, *provider, *model); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set model %s. Restart Eggy for this to take effect.", *alias), nil
}

func configSetCalendar(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set calendar", flag.ContinueOnError)
	enabled := flags.String("enabled", "", "true or false")
	defaultCalendar := flags.String("default-calendar", "", "default calendar ID")
	timezone := flags.String("timezone", "", "IANA timezone")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if err := bootstrap.SetCalendar(configPath, *enabled, *defaultCalendar, *timezone); err != nil {
		return "", err
	}
	return "Set calendar. Restart Eggy for this to take effect.", nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/eggy -count=1 -race`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eggy/config.go cmd/eggy/config_test.go
git commit -m "feat: add standalone eggy config CLI with flag-based syntax"
```

### Task 3: Wire `main.go`, update README, final verification

**Files:**
- Modify: `cmd/eggy/main.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `configMain` from Task 2.
- Produces: `eggy config ...` runnable end-to-end without constructing a `bootstrap.App`.

- [ ] **Step 1: Wire the `config` subcommand ahead of `App` construction**

In `cmd/eggy/main.go`, replace the `run` function in full:

```go
func run(arguments []string) error {
	flags := flag.NewFlagSet("eggy", flag.ContinueOnError)
	defaultConfig := os.Getenv("EGGY_CONFIG")
	if defaultConfig == "" {
		defaultConfig = "/data/config.yaml"
	}
	configPath := flags.String("config", defaultConfig, "path to config.yaml")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("usage: eggy [-config path] status|repositories|runs|stop <id>|schedules|memory|new|config")
	}
	if flags.Arg(0) == "config" {
		output, err := configMain(*configPath, flags.Args()[1:])
		if err != nil {
			return err
		}
		fmt.Println(output)
		return nil
	}
	envPath := os.Getenv("EGGY_ENV_FILE")
	if envPath == "" {
		envPath = ".env"
	}
	getenv, err := bootstrap.DotEnv(envPath, os.Getenv)
	if err != nil {
		return err
	}
	config, secrets, err := bootstrap.LoadOrCreateConfig(*configPath, getenv)
	if err != nil {
		return err
	}
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: true, ConfigPath: *configPath})
	if err != nil {
		return err
	}
	command := "/" + strings.Join(flags.Args(), " ")
	output, handled, err := app.ExecuteCommand(context.Background(), command)
	if err != nil {
		return err
	}
	if !handled {
		return fmt.Errorf("unknown command %q", flags.Arg(0))
	}
	fmt.Println(output)
	return nil
}
```

- [ ] **Step 2: Manually verify the CLI end to end**

Run:

```sh
mkdir -p /tmp/eggy-config-smoke
cat > /tmp/eggy-config-smoke/config.yaml <<'EOF'
version: 2
server:
  listen: ':8080'
  public_base_url: https://eggy.example
  telegram_webhook_path: /webhooks/telegram
data_dir: /tmp/eggy-config-smoke
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
  root: /tmp/eggy-config-smoke/runs
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
EOF
echo "--- config get coding (before) ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config get coding
echo "--- config set coding-agent claude ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config set coding-agent --alias=claude --adapter=claude_cli --credential-env=CLAUDE_CODE_OAUTH_TOKEN
echo "--- config get coding (after) ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config get coding
echo "--- config set calendar (patch) ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config set calendar --timezone=Asia/Singapore
echo "--- config get calendar ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config get calendar
echo "--- config show ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config show
echo "--- status still works (not routed through configMain) ---"
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml status
rm -rf /tmp/eggy-config-smoke
```

Expected: each `config get`/`set`/`show` prints the expected text as designed above; `status` still runs successfully through the normal `bootstrap.NewApp`/`app.ExecuteCommand` path, proving the new `config` branch in `run()` didn't disturb any other command.

- [ ] **Step 3: Update README**

In `README.md`, update the operational-shortcuts line. Change:

```markdown
Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, `/new`, `/model`, `/model <alias>`, `/model default`, `/coding_agent`, `/coding_agent <alias>`, `/coding_agent default`, `/config get <coding|providers|models>`, `/config set coding_agent <alias> <adapter> [credential_env]`, `/config set provider <name> <adapter> <base_url> <api_key_env>`, `/config set model <alias> <provider> <model_id>`, `/usage`, and `/usage reset`. Natural language remains the main interface.
```

to:

```markdown
Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, `/new`, `/model`, `/model <alias>`, `/model default`, `/coding_agent`, `/coding_agent <alias>`, `/coding_agent default`, `/config get <coding|providers|models|calendar|path>`, `/config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]`, `/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>`, `/config set model alias=<alias> provider=<provider> model=<model_id>`, `/config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]`, `/usage`, and `/usage reset`. Natural language remains the main interface.
```

Replace the Claude Code enablement paragraph in the Railway deployment section. Change:

```markdown
To enable Claude Code instead of, or alongside, Codex, generate a token locally with `claude setup-token` and set `CLAUDE_CODE_OAUTH_TOKEN` as a Railway service variable — never in `config.yaml`. Then register the alias with the owner-only Telegram command `/config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN` (or run `eggy config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN` from a checkout with `-config` pointed at the same `config.yaml`) — no SSH session required. Restart the service for the new alias to take effect. The token is valid for one year; renew it before expiry by running `claude setup-token` again and updating the Railway variable. Set `coding.default_agent` to `claude` to make it the default, or switch at runtime with `/coding_agent claude`.
```

to:

```markdown
To enable Claude Code instead of, or alongside, Codex, generate a token locally with `claude setup-token` and set `CLAUDE_CODE_OAUTH_TOKEN` as a Railway service variable — never in `config.yaml`. Then register the alias with the owner-only Telegram command `/config set coding_agent alias=claude adapter=claude_cli credential_env=CLAUDE_CODE_OAUTH_TOKEN`, or from the CLI with `eggy config set coding-agent --alias=claude --adapter=claude_cli --credential-env=CLAUDE_CODE_OAUTH_TOKEN` (pointed at the same `config.yaml` via `-config`) — no SSH session required. Restart the service for the new alias to take effect. The token is valid for one year; renew it before expiry by running `claude setup-token` again and updating the Railway variable. Set `coding.default_agent` to `claude` to make it the default, or switch at runtime with `/coding_agent claude`.
```

Replace the paragraph about editing the persisted YAML. Change:

```markdown
`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, use `/config set coding_agent`, `/config set provider`, or `/config set model` to register new entries in those sections, then restart. Other fields — branches, calendar settings, server URLs — still require editing the persisted YAML directly. API keys remain Railway variables and must not be copied into that file.
```

to:

```markdown
`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, use `/config set coding_agent`, `/config set provider`, `/config set model`, or `/config set calendar` (or the `eggy config set` CLI equivalents) to change those sections, then restart. Other fields — branches, server URLs — still require editing the persisted YAML directly. Run `eggy config show` to inspect the full file from a checkout with `-config` pointed at it. API keys remain Railway variables and must not be copied into that file.
```

- [ ] **Step 4: Run full verification**

Run: `make fmt vet test race build`

Expected: every command exits 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/eggy/main.go README.md
git commit -m "feat: wire eggy config CLI subcommand and document the refined /config syntax"
```
