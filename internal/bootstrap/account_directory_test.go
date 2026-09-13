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
    google_email: nigel@example.com
web:
  google_login:
    client_id: client
    client_secret_env: LOGIN_SECRET
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
	if _, ok := directory.AccountForEmail("new@example.com"); ok {
		t.Fatal("unknown account resolved")
	}
	if err := config.AddAccount(path, config.AccountInput{ID: "new", GoogleEmail: "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	if account, ok := directory.AccountForEmail("new@example.com"); !ok || account.ID != "new" {
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
