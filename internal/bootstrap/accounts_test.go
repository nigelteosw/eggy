package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// accountTestConfig is appTestConfig converted to accounts: two people, one
// of them the historical owner and bound to the environment login.
func accountTestConfig(dataDir string) config.Config {
	cfg := appTestConfig(dataDir)
	cfg.Owner = config.OwnerConfig{}
	cfg.Telegram = config.TelegramConfig{}
	cfg.Accounts = []config.AccountConfig{
		{ID: "nigel", TelegramUserID: 42},
		{ID: "partner"},
	}
	cfg.Web.PasswordAccountID = "nigel"
	cfg.MigrationOwnerID = "nigel"
	return cfg
}

func accountTestSecrets() config.Secrets {
	secrets := appTestSecrets("provider-secret")
	secrets.UIUserEmail = "owner@example.com"
	secrets.UIPassword = "operator-password"
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	return secrets
}

// Both shapes mount the same login: the accounts list and the legacy
// owner each answer the password route, and neither has a Google route.
func TestBothConfigShapesMountTheSameLogin(t *testing.T) {
	probe := func(t *testing.T, cfg config.Config, secrets config.Secrets, path string) int {
		t.Helper()
		app, err := NewApp(cfg, secrets, AppOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer app.database.Close()
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"username":"owner@example.com","password":"operator-password"}`)))
		return response.Code
	}
	legacySecrets := appTestSecrets("provider-secret")
	legacySecrets.UIUserEmail, legacySecrets.UIPassword = "owner@example.com", "operator-password"
	for name, tc := range map[string]struct {
		cfg     config.Config
		secrets config.Secrets
	}{"accounts": {accountTestConfig(t.TempDir()), accountTestSecrets()}, "legacy": {appTestConfig(t.TempDir()), legacySecrets}} {
		if code := probe(t, tc.cfg, tc.secrets, "/api/login"); code != http.StatusOK {
			t.Fatalf("%s login status=%d", name, code)
		}
		if code := probe(t, tc.cfg, tc.secrets, "/auth/google/start"); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Fatalf("%s google start status=%d", name, code)
		}
	}
}

// A schedule whose account was removed before it fired does not run, and is
// switched off rather than retried every minute for someone who is gone.
func TestScheduleForARemovedAccountIsDisabledNotRun(t *testing.T) {
	cfg := accountTestConfig(t.TempDir())
	app, err := NewApp(cfg, accountTestSecrets(), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.database.Close()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	partner := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	if err := app.scheduler.Add(partner, ports.Schedule{ID: "later", Kind: ports.ScheduleExact, Execution: ports.ScheduleExecutionMessage, Instruction: "remember", NextRun: now.Add(-time.Minute), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// The account disappears from config before the tick.
	app.config.Accounts = app.config.Accounts[:1]
	if err := app.onScheduleTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	app.workers.Wait()
	schedule, err := app.database.Schedules().Get(partner, "later")
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Enabled || !schedule.PendingRun.IsZero() {
		t.Fatalf("schedule after tick: %+v", schedule)
	}
	// Nothing was delivered anywhere, and nothing was written as partner.
	if recent, _ := app.database.RecentMessages(partner, "telegram", 10); len(recent) != 0 {
		t.Fatalf("removed account's schedule produced history: %+v", recent)
	}
}

// /mode and /model are each person's own: one account's choice is not the
// other's default.
func TestApprovalModeAndModelPreferencesArePerAccount(t *testing.T) {
	app, err := NewApp(accountTestConfig(t.TempDir()), accountTestSecrets(), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.database.Close()
	nigel := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "nigel"})
	partner := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
	if err := app.approvals.SetMode(nigel, ports.ModeStrict); err != nil {
		t.Fatal(err)
	}
	if mode, _ := app.approvals.Mode(partner); mode != ports.ModeNormal {
		t.Fatalf("partner's mode = %q after nigel chose strict", mode)
	}
	if mode, _ := app.approvals.Mode(nigel); mode != ports.ModeStrict {
		t.Fatalf("nigel's mode = %q", mode)
	}
	if _, _, err := app.ExecuteCommand(partner, "/mode auto"); err != nil {
		t.Fatal(err)
	}
	if mode, _ := app.approvals.Mode(nigel); mode != ports.ModeStrict {
		t.Fatalf("nigel's mode = %q after partner chose auto", mode)
	}
}
