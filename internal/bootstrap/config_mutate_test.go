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
