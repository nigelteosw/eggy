package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLoadOrCreateConfigGeneratesSafeDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	env := firstBootEnv()
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(env))
	if err != nil {
		t.Fatalf("LoadOrCreateConfig() error = %v", err)
	}
	if cfg.Telegram.OwnerID != 42 || cfg.Server.PublicBaseURL != "https://eggy.up.railway.app" {
		t.Fatalf("generated config = %#v", cfg)
	}
	if cfg.DataDir != "/data" || cfg.Server.TelegramWebhookPath != "/webhooks/telegram" || len(cfg.Repositories) != 0 {
		t.Fatalf("unsafe generated defaults = %#v", cfg)
	}
	provider, model, err := cfg.ActiveModel("deepseek-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.DefaultModel != "deepseek-pro" || provider.APIKeyEnv != "DEEPSEEK_API_KEY" || model.Model != "deepseek-v4-pro" {
		t.Fatalf("generated models = %#v %#v", provider, model)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "timeout: 45m0s") {
		t.Fatalf("durations were not encoded as strings:\n%s", body)
	}
	for _, secret := range testSecrets() {
		if strings.Contains(string(body), secret) {
			t.Fatal("generated config contains a provider secret")
		}
	}
	if _, _, err := LoadConfig(path, mapEnv(env)); err != nil {
		t.Fatalf("generated config did not strictly reload: %v", err)
	}
}

func TestLoadOrCreateConfigMigratesLegacyTemporaryRunnerRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := strings.Replace(validConfig(), "root: /data/runs", "root: /tmp/runs", 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.Root != "/data/runs" {
		t.Fatalf("runner root=%q", cfg.Runner.Root)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/tmp/runs") || !strings.Contains(string(body), "root: /data/runs") {
		t.Fatalf("migrated config:\n%s", body)
	}
}

// A config left by an older build carries settings whose fields are gone.
// Strict decoding would refuse it outright, so the retired keys are dropped on
// load and everything the owner still relies on survives, comments included.
func TestLoadOrCreateConfigDropsRetiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := validConfig() + `implementation_sessions:
  context_budget_chars: 96000
scheduler:
  heartbeat_cadence: 3h
  quiet_hours:
    start: '22:00'
calendar:
  enabled: true
  # Mutations without a calendar_id write here.
  default_calendar: primary
  timezone: Asia/Singapore
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatalf("LoadOrCreateConfig() error = %v", err)
	}
	if cfg.Calendar.DefaultCalendar != "primary" {
		t.Fatalf("calendar = %#v, want the surviving default_calendar", cfg.Calendar)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"implementation_sessions", "scheduler", "heartbeat_cadence", "enabled: true", "Asia/Singapore"} {
		if strings.Contains(string(body), retired) {
			t.Fatalf("retired %q survived:\n%s", retired, body)
		}
	}
	for _, kept := range []string{"default_calendar: primary", "# Mutations without a calendar_id write here.", "owner_id: 42", "clone_url: https://github.com/acme/repo.git"} {
		if !strings.Contains(string(body), kept) {
			t.Fatalf("pruning lost %q:\n%s", kept, body)
		}
	}
}

// calendar.timezone moved to agent.timezone rather than disappearing, so an
// older config keeps its clock instead of being dropped onto the UTC default.
func TestLoadOrCreateConfigCarriesCalendarTimezoneToAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := strings.Replace(validConfig(), "  timezone: UTC\n", "", 1) + `calendar:
  default_calendar: primary
  timezone: Asia/Singapore
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatalf("LoadOrCreateConfig() error = %v", err)
	}
	if cfg.Agent.Timezone != "Asia/Singapore" {
		t.Fatalf("agent.timezone = %q, want the calendar's carried over", cfg.Agent.Timezone)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "Asia/Singapore") != 1 {
		t.Fatalf("timezone was copied rather than moved:\n%s", body)
	}
}

// An agent.timezone already in the file is the current setting and wins.
func TestLoadOrCreateConfigKeepsExistingAgentTimezone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := validConfig() + "calendar:\n  default_calendar: primary\n  timezone: Asia/Singapore\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Timezone != "UTC" {
		t.Fatalf("agent.timezone = %q, want the config's own UTC", cfg.Agent.Timezone)
	}
}

// Pruning must not rewrite a config that has nothing retired in it: an
// untouched file keeps its formatting and its modification time.
func TestLoadOrCreateConfigLeavesCurrentConfigByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets())); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != validConfig() {
		t.Fatalf("current config was rewritten:\n%s", body)
	}
}

func TestLoadOrCreateConfigValidatesFirstBootEnvironment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"missing owner", func(values map[string]string) { delete(values, "EGGY_TELEGRAM_OWNER_ID") }, "EGGY_TELEGRAM_OWNER_ID is required, or EGGY_OWNER_ID for a web-only deployment"},
		{"invalid owner", func(values map[string]string) { values["EGGY_TELEGRAM_OWNER_ID"] = "not-a-number" }, "EGGY_TELEGRAM_OWNER_ID must be a positive integer"},
		{"zero owner", func(values map[string]string) { values["EGGY_TELEGRAM_OWNER_ID"] = "0" }, "EGGY_TELEGRAM_OWNER_ID must be a positive integer"},
		{"missing public URL", func(values map[string]string) { delete(values, "EGGY_PUBLIC_BASE_URL") }, "EGGY_PUBLIC_BASE_URL is required when RAILWAY_PUBLIC_DOMAIN is unavailable"},
		{"invalid public URL", func(values map[string]string) { values["EGGY_PUBLIC_BASE_URL"] = "ftp://invalid" }, "server.public_base_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			env := firstBootEnv()
			tt.mutate(env)
			_, _, err := LoadOrCreateConfig(path, mapEnv(env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("config exists after failed initialization: %v", statErr)
			}
		})
	}
}

func TestLoadOrCreateConfigUsesRailwayDomain(t *testing.T) {
	env := firstBootEnv()
	delete(env, "EGGY_PUBLIC_BASE_URL")
	env["RAILWAY_PUBLIC_DOMAIN"] = "eggy-production.up.railway.app"
	cfg, _, err := LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PublicBaseURL != "https://eggy-production.up.railway.app" {
		t.Fatalf("public base URL = %q", cfg.Server.PublicBaseURL)
	}
}

func TestLoadOrCreateConfigAddsOptionalRepository(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		env := firstBootEnv()
		env["EGGY_REPOSITORY_URL"] = "https://github.com/acme/project.git"
		env["EGGY_REPOSITORY_NAME"] = "project"
		env["EGGY_REPOSITORY_BASE_BRANCH"] = "trunk"
		env["EGGY_REPOSITORY_PROTECTED_BRANCHES"] = "trunk, release"
		cfg, _, err := LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		want := RepositoryConfig{Name: "project", CloneURL: "https://github.com/acme/project.git", BaseBranch: "trunk", ProtectedBranches: []string{"trunk", "release"}}
		if len(cfg.Repositories) != 1 || !repositoryConfigEqual(cfg.Repositories[0], want) {
			t.Fatalf("repositories = %#v, want %#v", cfg.Repositories, want)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		env := firstBootEnv()
		env["EGGY_REPOSITORY_URL"] = "https://github.com/acme/project.git"
		cfg, _, err := LoadOrCreateConfig(filepath.Join(t.TempDir(), "config.yaml"), mapEnv(env))
		if err != nil {
			t.Fatal(err)
		}
		want := RepositoryConfig{Name: "eggy", CloneURL: "https://github.com/acme/project.git", BaseBranch: "main", ProtectedBranches: []string{"main"}}
		if len(cfg.Repositories) != 1 || !repositoryConfigEqual(cfg.Repositories[0], want) {
			t.Fatalf("repositories = %#v, want %#v", cfg.Repositories, want)
		}
	})
}

func TestLoadOrCreateConfigNeverOverwritesExistingFile(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		before := []byte(validConfig())
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadOrCreateConfig(path, mapEnv(testSecrets())); err != nil {
			t.Fatal(err)
		}
		assertFileBytes(t, path, before)
	})
	t.Run("malformed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		before := []byte("invalid: yaml: [")
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadOrCreateConfig(path, mapEnv(firstBootEnv())); err == nil {
			t.Fatal("expected malformed existing config to fail")
		}
		assertFileBytes(t, path, before)
	})
}

func TestLoadOrCreateConfigSerializesConcurrentInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	env := firstBootEnv()
	start := make(chan struct{})
	errorsChannel := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, _, err := LoadOrCreateConfig(path, mapEnv(env))
			errorsChannel <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent initialization error = %v", err)
		}
	}
	if _, _, err := LoadConfig(path, mapEnv(env)); err != nil {
		t.Fatalf("final config did not strictly reload: %v", err)
	}
}

func firstBootEnv() map[string]string {
	values := testSecrets()
	values["EGGY_TELEGRAM_OWNER_ID"] = "42"
	values["EGGY_PUBLIC_BASE_URL"] = "https://eggy.up.railway.app"
	return values
}

func repositoryConfigEqual(got, want RepositoryConfig) bool {
	if got.Name != want.Name || got.CloneURL != want.CloneURL || got.BaseBranch != want.BaseBranch || len(got.ProtectedBranches) != len(want.ProtectedBranches) {
		return false
	}
	for index := range got.ProtectedBranches {
		if got.ProtectedBranches[index] != want.ProtectedBranches[index] {
			return false
		}
	}
	return true
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("file changed:\n%s", got)
	}
}

// A first boot with EGGY_OWNER_ID and no EGGY_TELEGRAM_OWNER_ID generates a
// web-only config: no telegram block, and no Telegram credentials needed.
func TestFirstBootGeneratesAWebOnlyConfigFromEGGYOwnerID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	env := firstBootEnv()
	delete(env, "EGGY_TELEGRAM_OWNER_ID")
	delete(env, "TELEGRAM_BOT_TOKEN")
	delete(env, "TELEGRAM_WEBHOOK_SECRET")
	env["EGGY_OWNER_ID"] = "owner-42"

	config, _, err := LoadOrCreateConfig(path, mapEnv(env))
	if err != nil {
		t.Fatalf("a web-only first boot must succeed: %v", err)
	}
	if config.Owner.ID != "owner-42" || config.Telegram.Configured() {
		t.Fatalf("owner=%#v telegram=%#v", config.Owner, config.Telegram)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "telegram:") {
		t.Fatalf("generated config must omit the telegram block:\n%s", body)
	}
	if _, _, err := LoadConfig(path, mapEnv(env)); err != nil {
		t.Fatalf("the generated web-only config must strictly reload: %v", err)
	}
}
