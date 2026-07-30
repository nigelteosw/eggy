package bootstrap

import (
	"net/http"
	"slices"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services/repo"
)

// TestPrimitiveToolsHaveExactlyOneDefinition is the guard on the unified,
// read-only repository surface.
func TestPrimitiveToolsHaveExactlyOneDefinition(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main"}}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range repo.PrimitiveNames {
		if got := count(names, name); got != 1 {
			t.Fatalf("primitive %q appears %d times in the tool surface, want exactly 1", name, got)
		}
	}
}

func TestRepositoryToolSurfaceIsReadOnly(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Repositories = []config.RepositoryConfig{{Name: "eggy", CloneURL: "https://github.com/nigelteosw/eggy.git", BaseBranch: "main"}}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range []string{"repository_list", "repository_github", "workspace_open", "read_file", "workspace_close"} {
		if !slices.Contains(names, name) {
			t.Fatalf("read-only repository tool %q is missing. names=%v", name, names)
		}
	}
	for _, gone := range []string{
		"terminal", "workspace_edit", "patch", "write_file", "propose_change",
		"skill_propose", "usage", "capabilities", "context",
	} {
		if slices.Contains(names, gone) {
			t.Fatalf("removed tool %q is still registered. names=%v", gone, names)
		}
	}
}

// TestCalendarToolsFollowTheConfigSection is the boundary check on Calendar
// being configurable rather than compiled in: an empty calendar section must
// cost nothing at all, and a configured one must expose the full set.
func TestCalendarToolsFollowTheConfigSection(t *testing.T) {
	calendarTools := []string{"calendar_list", "calendar_calendars", "calendar_create", "calendar_update", "calendar_delete"}

	unconfigured, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := unconfigured.loop.ToolNames(agent.RunOptions{})
	for _, name := range calendarTools {
		if slices.Contains(names, name) {
			t.Fatalf("unconfigured calendar registered %q. names=%v", name, names)
		}
	}

	cfg := appTestConfig(t.TempDir())
	cfg.Calendar = config.CalendarConfig{DefaultCalendar: "primary"}
	secrets := appTestSecrets("deepseek")
	secrets.GoogleClientID = "client-id"
	secrets.GoogleClientSecret = "client-secret"
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	configured, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names = configured.loop.ToolNames(agent.RunOptions{})
	for _, name := range calendarTools {
		if !slices.Contains(names, name) {
			t.Fatalf("configured calendar did not register %q. names=%v", name, names)
		}
	}
}

// TestCalendarMutationsKeepSeparateApprovalExecutors is the safety property
// that justifies Calendar being native at all: one approved action can never
// execute a different one.
func TestCalendarMutationsKeepSeparateApprovalExecutors(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.Calendar = config.CalendarConfig{DefaultCalendar: "primary"}
	secrets := appTestSecrets("deepseek")
	secrets.GoogleClientID = "client-id"
	secrets.GoogleClientSecret = "client-secret"
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []approvals.Action{approvals.CalendarCreate, approvals.CalendarUpdate, approvals.CalendarDelete} {
		if app.approvalExecutors[action] == nil {
			t.Fatalf("calendar action %q has no approval executor", action)
		}
	}
}

func TestRepositoryToolsAreAbsentWithoutConfiguredRepositories(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range []string{"repository_list", "repository_github", "workspace_open", "read_file", "workspace_close"} {
		if slices.Contains(names, name) {
			t.Fatalf("unconfigured repository tool %q is registered. names=%v", name, names)
		}
	}
}

func TestTelegramSelectToolOnlyExistsForARealConfiguredTelegramAdapter(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	secrets := appTestSecrets("deepseek")

	fakeApp, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fakeApp.loop.ToolNames(agent.RunOptions{}), "telegram_select") {
		t.Fatal("fake-adapter mode registered telegram_select")
	}

	webOnly := appTestConfig(t.TempDir())
	webOnly.Owner.ID = "owner"
	webOnly.Telegram = config.TelegramConfig{}
	webApp, err := NewApp(webOnly, config.Secrets{ProviderAPIKeys: secrets.ProviderAPIKeys}, AppOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(webApp.loop.ToolNames(agent.RunOptions{}), "telegram_select") {
		t.Fatal("web-only mode registered telegram_select")
	}

	httpClient := &http.Client{Transport: appRoundTrip(func(*http.Request) (*http.Response, error) {
		return appJSON(http.StatusOK, `{"ok":true,"result":true}`), nil
	})}
	telegramApp, err := NewApp(appTestConfig(t.TempDir()), secrets, AppOptions{HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(telegramApp.loop.ToolNames(agent.RunOptions{}), "telegram_select") {
		t.Fatal("configured Telegram adapter did not register telegram_select")
	}
}

func count(names []string, want string) int {
	total := 0
	for _, name := range names {
		if name == want {
			total++
		}
	}
	return total
}
