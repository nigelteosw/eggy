package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalAccountsNeedNoGoogleLogin(t *testing.T) {
	body := googleAccountConfig()
	begin := strings.Index(body, "  google_login:\n")
	if begin < 0 {
		t.Fatal("fixture lacks Google login section")
	}
	end := strings.Index(body[begin:], "    client_secret_env:")
	end += begin
	end += strings.Index(body[end:], "\n") + 1
	body = body[:begin] + "  password_account_id: nigel\n" + body[end:]
	body = strings.ReplaceAll(body, "    google_email: nigel@example.com\n", "")
	body = strings.ReplaceAll(body, "    google_email: Partner@Example.com\n", "")
	env := accountSecrets()
	env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
	env["EGGY_UI_PASSWORD"] = "existing-owner-password"
	if _, _, err := loadText(t, body, env); err != nil {
		t.Fatal(err)
	}
}

func TestLocalLoginShapeIsReportedBeforeStrictDecoding(t *testing.T) {
	_, _, err := loadText(t, googleAccountConfig(), accountSecrets())
	if !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("old shape: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(googleAccountConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDocument(path); !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("LoadDocument: %v", err)
	}
	// The boot path must not prune the retired keys before the cutover has
	// backed the file up.
	if _, _, err := LoadOrCreateConfig(path, mapEnv(accountSecrets())); !errors.Is(err, ErrLocalLoginMigrationRequired) {
		t.Fatalf("LoadOrCreateConfig: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "google_login") {
		t.Fatal("retired login fields were pruned before the migration")
	}
}

func TestMigrateLocalAccountsRewritesOnlyTheLoginShape(t *testing.T) {
	body := strings.Replace(googleAccountConfig(), "agent:", `# the shared Workspace grant stays exactly as written
google:
  enabled: true
  client_id: desktop-client
  client_secret_env: GOOGLE_CLIENT_SECRET
  expected_email: eggy@example.com
  products: [gmail]
agent:`, 1)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	env := accountSecrets()
	env["GOOGLE_CLIENT_SECRET"] = "desktop-secret"
	plan, err := PrepareLocalAccounts(path, "nigel", mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Accounts) != 2 || plan.PasswordAccountID != "nigel" || plan.Legacy || plan.ConfigDigest == "" {
		t.Fatalf("plan=%+v", plan)
	}
	if before, _ := os.ReadFile(path); string(before) != body {
		t.Fatal("preparation wrote the file")
	}
	if _, err := PrepareLocalAccounts(path, "stranger", mapEnv(env)); err == nil {
		t.Fatal("unknown password account accepted")
	}
	noLogin := testSecrets()
	noLogin["GOOGLE_CLIENT_SECRET"] = "desktop-secret"
	if _, err := PrepareLocalAccounts(path, "nigel", mapEnv(noLogin)); err == nil || !strings.Contains(err.Error(), "EGGY_UI_USER_EMAIL") {
		t.Fatalf("missing environment login: %v", err)
	}
	if err := MigrateLocalAccounts(path, "nigel", mapEnv(env)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	for _, gone := range []string{"google_email", "google_login", "client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET"} {
		if strings.Contains(text, gone) {
			t.Fatalf("%s survived the migration:\n%s", gone, text)
		}
	}
	for _, kept := range []string{"# the shared Workspace grant stays exactly as written", "client_id: desktop-client", "client_secret_env: GOOGLE_CLIENT_SECRET", "expected_email: eggy@example.com", "password_account_id: nigel", "telegram_user_id: 42", "protected_branches: [main]"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("%s missing after the migration:\n%s", kept, text)
		}
	}
	cfg, _, err := LoadConfig(path, mapEnv(env))
	if err != nil {
		t.Fatalf("migrated config does not load: %v", err)
	}
	if !cfg.TelegramEnabled() || cfg.PasswordAccountID() != "nigel" || !cfg.Google.Enabled {
		t.Fatalf("cfg=%+v", cfg)
	}
	// Rerunning is a no-op; rebinding to someone else is refused.
	if err := MigrateLocalAccounts(path, "nigel", mapEnv(env)); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if err := MigrateLocalAccounts(path, "partner", mapEnv(env)); err == nil {
		t.Fatal("rebinding through a rerun must fail")
	}
}

func TestMigrateLocalAccountsBindsTheLegacyOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareLocalAccounts(path, "42", mapEnv(accountSecrets()))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Legacy || len(plan.Accounts) != 1 || plan.Accounts[0].ID != "42" || plan.Accounts[0].TelegramUserID != 42 {
		t.Fatalf("plan=%+v", plan)
	}
	if _, err := PrepareLocalAccounts(path, "nigel", mapEnv(accountSecrets())); err == nil {
		t.Fatal("a legacy owner can only be bound under its own id")
	}
	if err := MigrateLocalAccounts(path, "42", mapEnv(accountSecrets())); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path, mapEnv(accountSecrets()))
	if err != nil || cfg.AccountMode() || cfg.PasswordAccountID() != "42" || cfg.Telegram.OwnerID != 42 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestWithAccountBlocksConcurrentMutation(t *testing.T) {
	path := writeAccountConfig(t)
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithAccount(path, "partner", func(cfg Config, account AccountConfig) error {
			if account.ID != "partner" || cfg.PasswordAccountID() != "nigel" {
				t.Errorf("resolved %+v in %+v", account, cfg.Web)
			}
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	mutated := make(chan error, 1)
	go func() { mutated <- EditAccount(path, "partner", AccountInput{TelegramUserID: 7}) }()
	select {
	case err := <-mutated:
		t.Fatalf("mutation finished while the read held the lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-mutated; err != nil {
		t.Fatal(err)
	}
	if err := WithAccount(path, "stranger", func(Config, AccountConfig) error { return nil }); err == nil {
		t.Fatal("unknown account resolved")
	}
	// Removal is visible to the next read, even before any restart.
	if err := RemoveAccount(path, "partner"); err != nil {
		t.Fatal(err)
	}
	if err := WithAccount(path, "partner", func(Config, AccountConfig) error { return nil }); err == nil {
		t.Fatal("removed account still resolved")
	}
}
