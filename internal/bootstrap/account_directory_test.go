package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
)

func liveAccountConfig() string {
	return `server:
  public_base_url: https://eggy.example
data_dir: /data
accounts:
  - id: nigel
web:
  password_account_id: nigel
agent:
  default_model: model
providers:
  provider:
    adapter: openai_compatible
    base_url: https://api.example.com
    api_key_env: MODEL_KEY
models:
  model:
    provider: provider
    model: model-id
repositories: []
runner:
  root: /data/runs
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
`
}

func TestAccountDirectoryResolvesLiveValidatedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(liveAccountConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := newAccountDirectory(path, nil, config.Config{})
	if _, ok := directory.Account("new"); ok {
		t.Fatal("unknown account resolved")
	}
	if directory.PasswordAccountID() != "nigel" {
		t.Fatalf("password account = %q", directory.PasswordAccountID())
	}
	if err := config.AddAccount(path, config.AccountInput{ID: "new", TelegramUserID: 5}); err != nil {
		t.Fatal(err)
	}
	if account, ok := directory.Account("new"); !ok || account.ID != "new" || account.TelegramUserID != 5 {
		t.Fatalf("added account = %#v, %v", account, ok)
	}
	if err := config.RemoveAccount(path, "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok := directory.Account("new"); ok {
		t.Fatal("removed account still resolved")
	}
}

func TestAccountDirectoryFailsClosedOnInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(liveAccountConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := newAccountDirectory(path, nil, config.Config{})
	if _, ok := directory.Account("nigel"); !ok {
		t.Fatal("valid account did not resolve")
	}
	if err := os.WriteFile(path, []byte("accounts: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := directory.Account("nigel"); ok || len(directory.Accounts()) != 0 {
		t.Fatal("invalid config authorized an account")
	}
}
