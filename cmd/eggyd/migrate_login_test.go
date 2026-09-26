package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

const oldLoginConfig = `server:
  listen: ':8080'
  public_base_url: https://eggy.example
data_dir: HOME
# people
accounts:
  - id: nigel
    google_email: nigel@example.com
    telegram_user_id: 42
  - id: partner
    google_email: partner@example.com
web:
  google_login:
    client_id: web-client
    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET
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
repositories: []
runner:
  root: HOME/runs
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
`

func migrationEnv() func(string) string {
	values := map[string]string{
		"TELEGRAM_BOT_TOKEN":      "telegram-token",
		"TELEGRAM_WEBHOOK_SECRET": "webhook-secret",
		"DEEPSEEK_API_KEY":        "deepseek-key",
		"EGGY_ENCRYPTION_KEY":     "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"EGGY_UI_USER_EMAIL":      "owner@example.com",
		"EGGY_UI_PASSWORD":        "operator-secret-value",
	}
	return func(name string) string { return values[name] }
}

// oldHome writes a home the previous release left behind: an old-shape
// config and a version-9 database with a session, a private trace, and a
// sealed outbound grant.
func oldHome(t *testing.T) home.Layout {
	t.Helper()
	layout := home.At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(oldLoginConfig, "HOME", layout.Root)
	if err := os.WriteFile(layout.Config(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Auth().Write("google", "workspace", []byte("sealed-grant")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Wind the fresh database back to the previous version's shape.
	db, err := sql.Open("sqlite", layout.Database())
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE account_auth`, `DROP TABLE web_login_links`,
		`UPDATE schema_meta SET value = '9' WHERE key = 'machine_state_version'`,
		`INSERT INTO sessions VALUES ('old-session', 'nigel', '2026-09-01T00:00:00Z', '2099-01-01T00:00:00Z')`,
		`INSERT INTO traces VALUES ('tr1', 'partner', 'owner', '', 'telegram', 'telegram', 'owner', 'm', '', 'private prompt', 'reply', '', '{}', 10, 5, 1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func assertMigrated(t *testing.T, layout home.Layout, stdout string) {
	t.Helper()
	for _, secret := range []string{"operator-secret-value", "deepseek-key", "MDEyMzQ1"} {
		if strings.Contains(stdout, secret) {
			t.Fatalf("secret on stdout: %s", stdout)
		}
	}
	cfg, _, err := config.LoadConfig(layout.Config(), migrationEnv())
	if err != nil {
		t.Fatalf("migrated config: %v", err)
	}
	if cfg.PasswordAccountID() != "nigel" || len(cfg.Accounts) != 2 || !cfg.TelegramEnabled() {
		t.Fatalf("cfg=%+v", cfg)
	}
	text, _ := os.ReadFile(layout.Config())
	if !strings.Contains(string(text), "# people") || strings.Contains(string(text), "google_email") {
		t.Fatalf("config after migration:\n%s", text)
	}
	backup, err := os.ReadFile(layout.Config() + localLoginBackupSuffix)
	if err != nil || !strings.Contains(string(backup), "google_login") {
		t.Fatalf("config backup: %v", err)
	}
	if err := sqlitestore.VerifyDatabaseBackup(context.Background(), layout.Database()+localLoginBackupSuffix); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(layout.Database())
	if err != nil {
		t.Fatalf("daemon open after cutover: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, id := range []string{"nigel", "partner"} {
		record, err := store.AccountAuth(ctx, id)
		if err != nil || record.Retired || record.PasswordHash != "" {
			t.Fatalf("seeded row %s=%+v err=%v", id, record, err)
		}
	}
	if grant, err := store.Auth().Read("google", "workspace"); err != nil || string(grant) != "sealed-grant" {
		t.Fatalf("grant=%q err=%v", grant, err)
	}
	if _, err := store.SessionAccount(ctx, "old-session", time.Now()); !errors.Is(err, sqlitestore.ErrSessionNotFound) {
		t.Fatalf("session issued under Google Sign-In survived: %v", err)
	}
	traces, err := store.ListTraces(ports.WithPrincipal(ctx, ports.Principal{AccountID: "partner"}), 10)
	if err != nil || len(traces) != 1 || traces[0].ID != "tr1" {
		t.Fatalf("private trace after cutover: %+v err=%v", traces, err)
	}
	if marker, found, err := store.LocalLoginCutover(ctx); err != nil || !found || marker.Phase != sqlitestore.CutoverComplete {
		t.Fatalf("marker=%+v found=%v err=%v", marker, found, err)
	}
}

func TestMigrateLocalLoginCompletesInOneRun(t *testing.T) {
	layout := oldHome(t)
	if _, err := sqlitestore.Open(layout.Database()); !errors.Is(err, sqlitestore.ErrLocalLoginMigrationRequired) {
		t.Fatalf("daemon opened the old home: %v", err)
	}
	var stdout bytes.Buffer
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, migrationHooks{}); err != nil {
		t.Fatal(err)
	}
	assertMigrated(t, layout, stdout.String())
	// A second run is a no-op that reports the same backups.
	stdout.Reset()
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, migrationHooks{}); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	assertMigrated(t, layout, stdout.String())
}

func TestMigrateLocalLoginRefusesBadInput(t *testing.T) {
	layout := oldHome(t)
	var stdout bytes.Buffer
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "", migrationEnv(), &stdout, migrationHooks{}); err == nil {
		t.Fatal("missing password account accepted")
	}
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "stranger", migrationEnv(), &stdout, migrationHooks{}); err == nil {
		t.Fatal("unknown password account accepted")
	}
	noLogin := func(name string) string {
		if name == "EGGY_UI_PASSWORD" {
			return ""
		}
		return migrationEnv()(name)
	}
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", noLogin, &stdout, migrationHooks{}); err == nil || !strings.Contains(err.Error(), "EGGY_UI_PASSWORD") {
		t.Fatalf("missing environment login: %v", err)
	}
	// Nothing was touched by the refusals.
	if _, err := os.Stat(layout.Config() + localLoginBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused run left a config backup")
	}
	if _, err := os.Stat(layout.Database() + localLoginBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused run left a database backup")
	}
	// Stray backups with no marker are refused rather than overwritten.
	if err := os.WriteFile(layout.Config()+localLoginBackupSuffix, []byte("somebody's copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, migrationHooks{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("stray backup: %v", err)
	}
	if body, _ := os.ReadFile(layout.Config() + localLoginBackupSuffix); string(body) != "somebody's copy" {
		t.Fatal("stray backup was overwritten")
	}
}

func TestMigrateLocalLoginResumesAfterEachInterruption(t *testing.T) {
	interrupted := errors.New("interrupted")
	cases := map[string]func(*migrationHooks){
		"after backup":   func(h *migrationHooks) { h.afterBackup = func() error { return interrupted } },
		"after database": func(h *migrationHooks) { h.afterDatabase = func() error { return interrupted } },
		"after config":   func(h *migrationHooks) { h.afterConfig = func() error { return interrupted } },
	}
	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			layout := oldHome(t)
			var hooks migrationHooks
			inject(&hooks)
			var stdout bytes.Buffer
			if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, hooks); !errors.Is(err, interrupted) {
				t.Fatalf("interruption: %v", err)
			}
			// The daemon refuses the half-migrated home.
			if _, err := sqlitestore.Open(layout.Database()); !errors.Is(err, sqlitestore.ErrLocalLoginMigrationRequired) {
				t.Fatalf("daemon opened a half-migrated home: %v", err)
			}
			if name == "after database" {
				// A session issued between the database upgrade and the rerun
				// must not be cleared by the rerun.
				store, err := sqlitestore.OpenForLocalAuthMigration(layout.Database())
				if err != nil {
					t.Fatal(err)
				}
				if err := store.CreateAuthenticatedSession(context.Background(), "between-runs", "nigel", 1, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stdout.Reset()
			if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, migrationHooks{}); err != nil {
				t.Fatalf("rerun: %v", err)
			}
			assertMigrated(t, layout, stdout.String())
			if name == "after database" {
				store, err := sqlitestore.Open(layout.Database())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if account, err := store.SessionAccount(context.Background(), "between-runs", time.Now()); err != nil || account != "nigel" {
					t.Fatalf("rerun cleared a session issued after the upgrade: account=%q err=%v", account, err)
				}
			}
		})
	}
}

func TestMigrateLocalLoginRefusesAConfigEditedAfterPreparation(t *testing.T) {
	layout := oldHome(t)
	interrupted := errors.New("interrupted")
	hooks := migrationHooks{afterBackup: func() error { return interrupted }}
	var stdout bytes.Buffer
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, hooks); !errors.Is(err, interrupted) {
		t.Fatalf("interruption: %v", err)
	}
	body, _ := os.ReadFile(layout.Config())
	if err := os.WriteFile(layout.Config(), append(body, "# edited after the backup\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "nigel", migrationEnv(), &stdout, migrationHooks{}); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("edited config: %v", err)
	}
}

func TestMigrateLocalLoginOnAFreshHomeRewritesOnlyTheConfig(t *testing.T) {
	layout := home.At(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(oldLoginConfig, "HOME", layout.Root)
	if err := os.WriteFile(layout.Config(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := migrateLocalLogin(context.Background(), layout, layout.Config(), "partner", migrationEnv(), &stdout, migrationHooks{}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadConfig(layout.Config(), migrationEnv())
	if err != nil || cfg.PasswordAccountID() != "partner" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := os.Stat(layout.Config() + localLoginBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh home got a backup")
	}
	if _, err := os.Stat(filepath.Join(layout.Root, "eggy.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh home got a database")
	}
}
