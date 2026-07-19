# Config Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add owner-only `/config get` and `/config set` commands, available identically through Telegram and the `eggy` CLI, so the owner can register new `coding.agents`, `providers`, and `models` entries in `config.yaml` without SSH access to the container.

**Architecture:** A new `internal/bootstrap/config_mutate.go` owns reading, mutating, validating, and atomically rewriting `config.yaml` for three whitelisted sections. `CommandService` (already shared by Telegram and the `eggy` CLI through `App.ExecuteCommand`) gains a `/config` case that dispatches to typed setter/getter functions, one per entity. No new component touches the running `App`, the runner, or `state.json` — a restart is still required for a written change to take effect, and the command says so.

**Tech Stack:** Go 1.26 standard library, `gopkg.in/yaml.v3`, the existing `internal/adapters/filelock` package.

## Global Constraints

- `/config set` only ever writes `coding.agents.*`, `providers.*`, `models.*`. Never `telegram.owner_id`, `server.*`, `calendar.*`, `runner.*`, or any other field.
- `/config set` requires a Version 2 config file. Reject Version 1 immediately with a clear "migrate to version 2" error before attempting any mutation; the file must be left untouched.
- Never print a secret value through `/config get` or a `/config set` confirmation — only adapter names, URLs, and environment-variable *names* ever appear in output.
- Reuse existing machinery: `Config.Validate()` for structural checks, `Config.MarshalYAML()` for serialization, and the `filelock.With` + temp-file + `os.Rename` atomic-write sequence already used in `internal/bootstrap/config_init.go`. Do not add new validation or locking logic.
- Write each behavior test-first and run `make fmt vet test race build` before completion.

---

### Task 1: Config mutation core

**Files:**
- Create: `internal/bootstrap/config_mutate.go`
- Create: `internal/bootstrap/config_mutate_test.go`

**Interfaces:**
- Consumes: `Config`, `CodingAgentConfig`, `ProviderConfig`, `ModelAliasConfig`, `configV2Document`, `legacyConfigDocument`, `decodeKnownYAML(data []byte, destination any) error`, `normalizeV2Config(configV2Document) Config`, `normalizeLegacyConfig(legacyConfigDocument) Config`, `Config.Validate() error`, `Config.MarshalYAML() (any, error)` — all in `internal/bootstrap/config.go`; `filelock.With(path string, operation func() error) error` from `internal/adapters/filelock`.
- Produces: `loadConfigDocument(path string) (Config, int, error)`, `setCodingAgent(path, alias, adapter, credentialEnv string) error`, `setProvider(path, name, adapter, baseURL, apiKeyEnv string) error`, `setModelAlias(path, alias, provider, modelID string) error`, `getCodingConfigText(path string) (string, error)`, `getProvidersConfigText(path string) (string, error)`, `getModelAliasesConfigText(path string) (string, error)`.

- [ ] **Step 1: Write failing tests**

Create `internal/bootstrap/config_mutate_test.go`:

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
	if err := setCodingAgent(path, "claude", "claude_cli", "CLAUDE_CODE_OAUTH_TOKEN"); err != nil {
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

	if err := setCodingAgent(path, "claude", "claude_cli", "OTHER_TOKEN_ENV"); err != nil {
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
	err := setCodingAgent(path, "claude", "not_a_real_adapter", "")
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
	err := setCodingAgent(path, "claude", "claude_cli", "CLAUDE_CODE_OAUTH_TOKEN")
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
	if err := setProvider(path, "openrouter", "openai_compatible", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"); err != nil {
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
	err = setProvider(path, "broken", "openai_compatible", "not-a-url", "BROKEN_API_KEY")
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
	if err := setModelAlias(path, "deepseek-fast", "deepseek", "deepseek-v4-flash"); err != nil {
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
	err = setModelAlias(path, "orphan", "does-not-exist", "some-model")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("error = %v", err)
	}
	assertFileBytes(t, path, before)
}

func TestGetConfigTextFormatsEachSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigV2()), 0o600); err != nil {
		t.Fatal(err)
	}
	coding, err := getCodingConfigText(path)
	if err != nil || coding != "default_agent: codex\ncodex  adapter=codex_cli" {
		t.Fatalf("coding text = %q, err=%v", coding, err)
	}
	providers, err := getProvidersConfigText(path)
	if err != nil || providers != "deepseek  adapter=openai_compatible  base_url=https://api.deepseek.com  api_key_env=DEEPSEEK_API_KEY" {
		t.Fatalf("providers text = %q, err=%v", providers, err)
	}
	models, err := getModelAliasesConfigText(path)
	if err != nil || models != "deepseek-pro  provider=deepseek  model=deepseek-v4-pro" {
		t.Fatalf("models text = %q, err=%v", models, err)
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
			errorsChannel <- setCodingAgent(path, alias, "codex_cli", "")
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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bootstrap -run 'SetCodingAgent|SetProvider|SetModelAlias|GetConfigText' -count=1`

Expected: compilation fails — `setCodingAgent`, `setProvider`, `setModelAlias`, `getCodingConfigText`, `getProvidersConfigText`, `getModelAliasesConfigText`, and `loadConfigDocument` do not exist yet.

- [ ] **Step 3: Implement `internal/bootstrap/config_mutate.go`**

```go
package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func setCodingAgent(path, alias, adapter, credentialEnv string) error {
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

func setProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
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

func setModelAlias(path, alias, provider, modelID string) error {
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

func getCodingConfigText(path string) (string, error) {
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

func getProvidersConfigText(path string) (string, error) {
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

func getModelAliasesConfigText(path string) (string, error) {
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bootstrap -run 'SetCodingAgent|SetProvider|SetModelAlias|GetConfigText' -count=1 -race`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/config_mutate.go internal/bootstrap/config_mutate_test.go
git commit -m "feat: add config.yaml mutation core for coding agents, providers, and models"
```

### Task 2: `/config` command and bootstrap wiring

**Files:**
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`

**Interfaces:**
- Consumes: `setCodingAgent`, `setProvider`, `setModelAlias`, `getCodingConfigText`, `getProvidersConfigText`, `getModelAliasesConfigText` from Task 1.
- Produces: `CommandService.configPath string` field; `AppOptions.ConfigPath string` field; the `/config get <coding|providers|models>` and `/config set <coding_agent|provider|model> ...` commands, available through both `App.ExecuteCommand` (Telegram) and `eggy config ...` (CLI, once Task 3 wires the flag).

- [ ] **Step 1: Write failing `CommandService` tests**

Add to `internal/bootstrap/commands_test.go` (add `"os"` and `"path/filepath"` to the existing import block if not already present):

```go
func TestCommandConfigReportsUnconfigured(t *testing.T) {
	commands := &CommandService{}
	output, handled, err := commands.Execute(context.Background(), "/config get coding")
	if err != nil || !handled || output != "Config file management is not configured." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
}

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

	output, _, err = commands.Execute(ctx, "/config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN")
	if err != nil || output != "Set coding agent claude. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get coding")
	if err != nil || output != "default_agent: codex\nclaude  adapter=claude_cli  credential_env=CLAUDE_CODE_OAUTH_TOKEN\ncodex  adapter=codex_cli" {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set coding_agent claude bad_adapter")
	if err != nil || !strings.Contains(output, "unsupported coding agent adapter") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set provider openrouter openai_compatible https://openrouter.ai/api/v1 OPENROUTER_API_KEY")
	if err != nil || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get providers")
	if err != nil || !strings.Contains(output, "openrouter  adapter=openai_compatible  base_url=https://openrouter.ai/api/v1  api_key_env=OPENROUTER_API_KEY") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model openrouter-pro openrouter your-model-id")
	if err != nil || output != "Set model openrouter-pro. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config get models")
	if err != nil || !strings.Contains(output, "openrouter-pro  provider=openrouter  model=your-model-id") {
		t.Fatalf("output=%q err=%v", output, err)
	}

	output, _, err = commands.Execute(ctx, "/config set model orphan missing-provider some-model")
	if err != nil || !strings.Contains(output, "unknown provider") {
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
		{"/config", "Usage: /config get <coding|providers|models>|set <coding_agent|provider|model> ..."},
		{"/config get", "Usage: /config get <coding|providers|models>"},
		{"/config get unknown", "Usage: /config get <coding|providers|models>"},
		{"/config set", "Usage: /config set <coding_agent|provider|model> ..."},
		{"/config set coding_agent claude", "Usage: /config set coding_agent <alias> <adapter> [credential_env]"},
		{"/config set provider openrouter openai_compatible", "Usage: /config set provider <name> <adapter> <base_url> <api_key_env>"},
		{"/config set model openrouter-pro openrouter", "Usage: /config set model <alias> <provider> <model_id>"},
	}
	for _, tt := range tests {
		output, handled, err := commands.Execute(ctx, tt.input)
		if err != nil || !handled || output != tt.want {
			t.Fatalf("input=%q output=%q handled=%v err=%v", tt.input, output, handled, err)
		}
	}
}
```

- [ ] **Step 2: Write a failing `App`-level wiring test**

Add to `internal/bootstrap/app_test.go` (add `"gopkg.in/yaml.v3"` to the existing import block):

```go
func TestAppConfigSetWritesToConfiguredPath(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.yaml")
	cfg := appTestConfig(dataDir)
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(cfg, appTestSecrets("key"), AppOptions{FakeAdapters: true, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	output, handled, err := app.ExecuteCommand(context.Background(), "/config set provider openrouter openai_compatible https://openrouter.ai/api/v1 OPENROUTER_API_KEY")
	if err != nil || !handled || output != "Set provider openrouter. Restart Eggy for this to take effect." {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	reloaded, _, err := LoadConfig(configPath, mapEnv(map[string]string{"TELEGRAM_BOT_TOKEN": "bot", "TELEGRAM_WEBHOOK_SECRET": "webhook", "DEEPSEEK_API_KEY": "key"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Providers["openrouter"]; !ok {
		t.Fatalf("providers = %#v", reloaded.Providers)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/bootstrap -run 'CommandConfig|AppConfigSet' -count=1`

Expected: compilation fails — `CommandService` has no field `configPath`, `AppOptions` has no field `ConfigPath`, and `/config` is not handled.

- [ ] **Step 4: Add the `configPath` field and `/config` case to `CommandService`**

In `internal/bootstrap/commands.go`, add `configPath string` to the `CommandService` struct (after `defaultCodingAgent string`):

```go
type CommandService struct {
	config             Config
	store              ports.StateStore
	context            ports.ContextStore
	conversation       *services.ConversationService
	coding             *services.CodingService
	repositories       *services.RepositoriesService
	agentRuntime       *services.AgentRuntime
	codingRuntime      *services.CodingAgentRuntime
	channel            ports.Channel
	owner              string
	defaultModel       string
	defaultCodingAgent string
	configPath         string
	modelAliases       []string
	now                func() time.Time
}
```

Add a new case in the `Execute` switch, immediately after the existing `case "/coding_agent":` block and before `case "/usage":`:

```go
	case "/config":
		if s.configPath == "" {
			return "Config file management is not configured.", true, nil
		}
		if len(fields) < 2 {
			return "Usage: /config get <coding|providers|models>|set <coding_agent|provider|model> ...", true, nil
		}
		switch fields[1] {
		case "get":
			if len(fields) != 3 {
				return "Usage: /config get <coding|providers|models>", true, nil
			}
			switch fields[2] {
			case "coding":
				text, err := getCodingConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "providers":
				text, err := getProvidersConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			case "models":
				text, err := getModelAliasesConfigText(s.configPath)
				if err != nil {
					return err.Error(), true, nil
				}
				return text, true, nil
			default:
				return "Usage: /config get <coding|providers|models>", true, nil
			}
		case "set":
			if len(fields) < 3 {
				return "Usage: /config set <coding_agent|provider|model> ...", true, nil
			}
			switch fields[2] {
			case "coding_agent":
				if len(fields) < 5 || len(fields) > 6 {
					return "Usage: /config set coding_agent <alias> <adapter> [credential_env]", true, nil
				}
				alias, adapter := fields[3], fields[4]
				credentialEnv := ""
				if len(fields) == 6 {
					credentialEnv = fields[5]
				}
				if err := setCodingAgent(s.configPath, alias, adapter, credentialEnv); err != nil {
					return err.Error(), true, nil
				}
				return "Set coding agent " + alias + ". Restart Eggy for this to take effect.", true, nil
			case "provider":
				if len(fields) != 7 {
					return "Usage: /config set provider <name> <adapter> <base_url> <api_key_env>", true, nil
				}
				name, adapter, baseURL, apiKeyEnv := fields[3], fields[4], fields[5], fields[6]
				if err := setProvider(s.configPath, name, adapter, baseURL, apiKeyEnv); err != nil {
					return err.Error(), true, nil
				}
				return "Set provider " + name + ". Restart Eggy for this to take effect.", true, nil
			case "model":
				if len(fields) != 6 {
					return "Usage: /config set model <alias> <provider> <model_id>", true, nil
				}
				alias, provider, modelID := fields[3], fields[4], fields[5]
				if err := setModelAlias(s.configPath, alias, provider, modelID); err != nil {
					return err.Error(), true, nil
				}
				return "Set model " + alias + ". Restart Eggy for this to take effect.", true, nil
			default:
				return "Usage: /config set <coding_agent|provider|model> ...", true, nil
			}
		default:
			return "Usage: /config get <coding|providers|models>|set <coding_agent|provider|model> ...", true, nil
		}
```

- [ ] **Step 5: Wire `ConfigPath` through `AppOptions` and `App`**

In `internal/bootstrap/app.go`, add `ConfigPath string` to `AppOptions` (after `ClaudeExecutable string`):

```go
type AppOptions struct {
	HTTPClient       *http.Client
	TelegramBaseURL  string
	ProviderBaseURLs map[string]string
	GitHubAPIBase    string
	GoogleAuthURL    string
	GoogleTokenURL   string
	GoogleAPIBase    string
	CodexExecutable  string
	ClaudeExecutable string
	ConfigPath       string
	Now              func() time.Time
	Logger           *slog.Logger
	FakeAdapters     bool
}
```

Add `configPath: options.ConfigPath` to the `CommandService` construction (the line currently reading `app.commands = &CommandService{config: config, ...}`):

```go
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, codingRuntime: app.codingRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, defaultCodingAgent: config.Coding.DefaultAgent, configPath: options.ConfigPath, modelAliases: aliases, now: options.Now}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/bootstrap -run 'CommandConfig|AppConfigSet' -count=1 -race`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/commands.go internal/bootstrap/commands_test.go internal/bootstrap/app.go internal/bootstrap/app_test.go
git commit -m "feat: add owner-only /config get and /config set commands"
```

### Task 3: CLI/daemon wiring, README, and final verification

**Files:**
- Modify: `cmd/eggy/main.go`
- Modify: `cmd/eggyd/main.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `bootstrap.AppOptions.ConfigPath` from Task 2.
- Produces: `eggy config get coding`, `eggy config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN`, etc., runnable from the command line against the same `config.yaml` the daemon uses.

- [ ] **Step 1: Pass `ConfigPath` from `cmd/eggy/main.go`**

In `internal/bootstrap` there are no changes here — only the two `main.go` files. In `cmd/eggy/main.go`, change:

```go
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: true})
```

to:

```go
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: true, ConfigPath: *configPath})
```

Also update the usage error a few lines above it, changing:

```go
		return fmt.Errorf("usage: eggy [-config path] status|repositories|runs|stop <id>|schedules|memory|new")
```

to:

```go
		return fmt.Errorf("usage: eggy [-config path] status|repositories|runs|stop <id>|schedules|memory|new|config")
```

- [ ] **Step 2: Pass `ConfigPath` from `cmd/eggyd/main.go`**

In `cmd/eggyd/main.go`, change:

```go
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: getenv("EGGY_FAKE_ADAPTERS") == "1"})
```

to:

```go
	app, err := bootstrap.NewApp(config, secrets, bootstrap.AppOptions{FakeAdapters: getenv("EGGY_FAKE_ADAPTERS") == "1", ConfigPath: *configPath})
```

- [ ] **Step 3: Manually verify the CLI end to end**

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
  enabled: false
  default_calendar: primary
  timezone: UTC
EOF
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config get coding
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN
go run ./cmd/eggy -config /tmp/eggy-config-smoke/config.yaml config get coding
rm -rf /tmp/eggy-config-smoke
```

Expected: the first `config get coding` prints `default_agent: codex` and a `codex` line; the `config set` prints `Set coding agent claude. Restart Eggy for this to take effect.`; the second `config get coding` shows both `claude` and `codex` entries.

- [ ] **Step 4: Update README**

In `README.md`, update the operational shortcuts line to include the new commands. Change:

```markdown
Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, `/new`, `/model`, `/model <alias>`, `/model default`, `/coding_agent`, `/coding_agent <alias>`, `/coding_agent default`, `/usage`, and `/usage reset`. Natural language remains the main interface.
```

to:

```markdown
Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, `/new`, `/model`, `/model <alias>`, `/model default`, `/coding_agent`, `/coding_agent <alias>`, `/coding_agent default`, `/config get <coding|providers|models>`, `/config set coding_agent <alias> <adapter> [credential_env]`, `/config set provider <name> <adapter> <base_url> <api_key_env>`, `/config set model <alias> <provider> <model_id>`, `/usage`, and `/usage reset`. Natural language remains the main interface.
```

Replace the SSH-based Claude Code enablement paragraph in the Railway deployment section. Change:

```markdown
To enable Claude Code instead of, or alongside, Codex, add a `claude` alias under `coding.agents` in the persisted `/data/config.yaml` with `credential_env: CLAUDE_CODE_OAUTH_TOKEN`, then generate a token locally with `claude setup-token` and set `CLAUDE_CODE_OAUTH_TOKEN` as a Railway service variable — never in `config.yaml`. The token is valid for one year; renew it before expiry by running `claude setup-token` again and updating the Railway variable. Set `coding.default_agent` to `claude` to make it the default, or switch at runtime with `/coding_agent claude`.
```

to:

```markdown
To enable Claude Code instead of, or alongside, Codex, generate a token locally with `claude setup-token` and set `CLAUDE_CODE_OAUTH_TOKEN` as a Railway service variable — never in `config.yaml`. Then register the alias with the owner-only Telegram command `/config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN` (or run `eggy config set coding_agent claude claude_cli CLAUDE_CODE_OAUTH_TOKEN` from a checkout with `-config` pointed at the same `config.yaml`) — no SSH session required. Restart the service for the new alias to take effect. The token is valid for one year; renew it before expiry by running `claude setup-token` again and updating the Railway variable. Set `coding.default_agent` to `claude` to make it the default, or switch at runtime with `/coding_agent claude`.
```

Update the paragraph about editing the persisted YAML. Change:

```markdown
`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, edit the persisted YAML to change providers, aliases, repositories, or branches, then redeploy. API keys remain Railway variables and must not be copied into that file.
```

to:

```markdown
`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, use `/config set coding_agent`, `/config set provider`, or `/config set model` to register new entries in those sections, then restart. Other fields — branches, calendar settings, server URLs — still require editing the persisted YAML directly. API keys remain Railway variables and must not be copied into that file.
```

- [ ] **Step 5: Run full verification**

Run: `make fmt vet test race build`

Expected: every command exits 0.

- [ ] **Step 6: Commit**

```bash
git add cmd/eggy/main.go cmd/eggyd/main.go README.md
git commit -m "feat: wire /config command through the CLI and document it"
```
