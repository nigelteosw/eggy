package bootstrap

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
	if cfg.Version != 2 || cfg.Telegram.OwnerID != 42 || cfg.Server.PublicBaseURL != "https://eggy.up.railway.app" {
		t.Fatalf("generated config = %#v", cfg)
	}
	if cfg.DataDir != "/data" || cfg.Server.TelegramWebhookPath != "/webhooks/telegram" || cfg.Calendar.Enabled || len(cfg.Repositories) != 0 {
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
	if !strings.Contains(string(body), "timeout: 45m0s") || !strings.Contains(string(body), "heartbeat_cadence: 30m0s") {
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

func TestLoadOrCreateConfigValidatesFirstBootEnvironment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"missing owner", func(values map[string]string) { delete(values, "EGGY_TELEGRAM_OWNER_ID") }, "EGGY_TELEGRAM_OWNER_ID is required"},
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
