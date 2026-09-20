package bootstrap

import (
	"context"
	"fmt"
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

// Personal runtime settings belong to the account, not to the session: they
// survive a password reset and a fresh sign-in, a newly created person
// starts from the deployment defaults, and none of it touches the shared
// provider configuration.
func TestPersonalSettingsFollowTheAccountAndLeaveSharedConfigAlone(t *testing.T) {
	app, err := NewApp(accountTestConfig(t.TempDir()), accountTestSecrets(), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.database.Close()
	ctx := context.Background()
	nigel := ports.WithPrincipal(ctx, ports.Principal{AccountID: "nigel"})
	partner := ports.WithPrincipal(ctx, ports.Principal{AccountID: "partner"})
	before := app.config.Providers

	if err := app.runtime.SetShowThinking(nigel, false); err != nil {
		t.Fatal(err)
	}
	if err := app.approvals.SetMode(nigel, ports.ModeAuto); err != nil {
		t.Fatal(err)
	}
	if show, _ := app.runtime.ShowThinking(partner); !show {
		t.Fatal("partner lost thinking because nigel hid theirs")
	}
	if mode, _ := app.approvals.Mode(partner); mode != ports.ModeNormal {
		t.Fatalf("partner's mode=%q after nigel chose auto", mode)
	}
	// A password reset and revocation change credentials and sessions,
	// not preferences.
	record, err := app.database.AccountAuth(ctx, "nigel")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.database.SetAccountPassword(ctx, "nigel", "encoded", record.Generation); err != nil {
		t.Fatal(err)
	}
	if err := app.database.RevokeAccountAuth(ctx, "nigel"); err != nil {
		t.Fatal(err)
	}
	if show, _ := app.runtime.ShowThinking(nigel); show {
		t.Fatal("nigel's thinking preference was lost by a credential change")
	}
	if mode, _ := app.approvals.Mode(nigel); mode != ports.ModeAuto {
		t.Fatalf("nigel's mode=%q after a credential change", mode)
	}
	// A new person inherits nothing.
	third := ports.WithPrincipal(ctx, ports.Principal{AccountID: "third"})
	if err := initializeAccountState(app.database.State(), nil, "third"); err != nil {
		t.Fatal(err)
	}
	if show, _ := app.runtime.ShowThinking(third); !show {
		t.Fatal("a new account inherited hidden thinking")
	}
	if mode, _ := app.approvals.Mode(third); mode != ports.ModeNormal {
		t.Fatalf("a new account inherited mode %q", mode)
	}
	if fmt.Sprint(app.config.Providers) != fmt.Sprint(before) {
		t.Fatal("personal settings changed the shared provider configuration")
	}
}
