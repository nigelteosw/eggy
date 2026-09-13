package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSetupInput() SetupInput {
	return SetupInput{
		AccountID:            "you",
		GoogleEmail:          "you@example.com",
		PublicBaseURL:        "https://eggy.example.com",
		LoginClientID:        "login-client",
		LoginClientSecretEnv: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET",
		ProviderName:         "deepseek",
		ProviderBaseURL:      "https://api.deepseek.com",
		ProviderAPIKeyEnv:    "DEEPSEEK_API_KEY",
		ModelAlias:           "deepseek-pro",
		ModelID:              "deepseek-v4-pro",
	}
}

func setupEnv() map[string]string {
	return map[string]string{
		"EGGY_ENCRYPTION_KEY":             "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"EGGY_GOOGLE_LOGIN_CLIENT_SECRET": "login-secret-value",
		"DEEPSEEK_API_KEY":                "provider-secret-value",
	}
}

func TestCompleteSetupWritesOnlyConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	input := validSetupInput()
	values := setupEnv()
	getenv := mapEnv(values)
	if err := CompleteSetup(root, configPath, input, getenv); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{input.ProviderAPIKeyEnv, input.LoginClientSecretEnv, "EGGY_ENCRYPTION_KEY"} {
		if value := getenv(name); value != "" && bytes.Contains(body, []byte(value)) {
			t.Fatalf("secret from %s written to YAML", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup created .env: %v", err)
	}
	cfg, _, err := LoadConfig(configPath, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].ID != "you" {
		t.Fatalf("accounts = %#v", cfg.Accounts)
	}
	if cfg.DataDir != root || cfg.Runner.Root != filepath.Join(root, "runs") {
		t.Fatalf("paths = data %q runner %q", cfg.DataDir, cfg.Runner.Root)
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %v, %v", info, err)
	}
}

func TestCompleteSetupLeavesNoFilesWhenCandidateIsInvalid(t *testing.T) {
	root := t.TempDir()
	input := validSetupInput()
	input.GoogleEmail = "not-an-email"
	err := CompleteSetup(root, filepath.Join(root, "config.yaml"), input, mapEnv(setupEnv()))
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, name := range []string{"config.yaml", ".env"} {
		if _, statErr := os.Stat(filepath.Join(root, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s exists: %v", name, statErr)
		}
	}
}

func TestCompleteSetupRequiresOnlyConfiguredSecretsAndPreservesDotEnv(t *testing.T) {
	root := t.TempDir()
	dotenvPath := filepath.Join(root, ".env")
	before := []byte("UNRELATED=keep-me\n")
	if err := os.WriteFile(dotenvPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSetup(root, filepath.Join(root, "config.yaml"), validSetupInput(), mapEnv(setupEnv())); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(dotenvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf(".env changed: %q", after)
	}
}

func TestValidateSetupReportsMissingRequiredVariables(t *testing.T) {
	tests := []struct {
		name string
		drop string
	}{
		{"encryption", "EGGY_ENCRYPTION_KEY"},
		{"login", "EGGY_GOOGLE_LOGIN_CLIENT_SECRET"},
		{"provider", "DEEPSEEK_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := setupEnv()
			delete(values, tt.drop)
			_, err := ValidateSetup(t.TempDir(), validSetupInput(), mapEnv(values))
			if err == nil || !strings.Contains(err.Error(), tt.drop) {
				t.Fatalf("error = %v, want %s", err, tt.drop)
			}
		})
	}
}

func TestCompleteSetupRefusesExistingConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	before := []byte("claimed: true\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSetup(root, path, validSetupInput(), mapEnv(setupEnv())); !errors.Is(err, ErrSetupAlreadyCompleted) {
		t.Fatalf("error = %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatalf("existing config changed: %q", after)
	}
}
