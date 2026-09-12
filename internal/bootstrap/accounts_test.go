package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// accountTestConfig is appTestConfig converted to accounts: two people, one
// of them the historical owner, and the login client accounts require.
func accountTestConfig(dataDir string) config.Config {
	cfg := appTestConfig(dataDir)
	cfg.Owner = config.OwnerConfig{}
	cfg.Telegram = config.TelegramConfig{}
	cfg.Accounts = []config.AccountConfig{
		{ID: "nigel", GoogleEmail: "nigel@example.com", TelegramUserID: 42},
		{ID: "partner", GoogleEmail: "partner@example.com"},
	}
	cfg.Web.GoogleLogin = config.GoogleLoginConfig{ClientID: "web-client", ClientSecretEnv: "EGGY_GOOGLE_LOGIN_CLIENT_SECRET"}
	cfg.MigrationOwnerID = "nigel"
	return cfg
}

func accountTestSecrets() config.Secrets {
	secrets := appTestSecrets("provider-secret")
	secrets.GoogleLoginClientSecret = "login-secret"
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	return secrets
}

func TestAccountModeMountsGoogleSignInAndLegacyModeDoesNot(t *testing.T) {
	probe := func(t *testing.T, cfg config.Config, secrets config.Secrets) int {
		t.Helper()
		app, err := NewApp(cfg, secrets, AppOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer app.database.Close()
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/google/start", nil))
		return response.Code
	}
	if code := probe(t, accountTestConfig(t.TempDir()), accountTestSecrets()); code != http.StatusFound {
		t.Fatalf("account mode start status=%d", code)
	}
	if code := probe(t, appTestConfig(t.TempDir()), appTestSecrets("provider-secret")); code != http.StatusNotFound {
		t.Fatalf("legacy mode start status=%d", code)
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
