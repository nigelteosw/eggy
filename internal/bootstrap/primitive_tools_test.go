package bootstrap

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
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

// TestNoCalendarToolsSurvive guards the deletion of the native Calendar
// adapter: a calendar server is configured under mcp like any other
// capability, so nothing may register a compiled-in calendar tool again.
func TestNoCalendarToolsSurvive(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range app.loop.ToolNames(agent.RunOptions{}) {
		if strings.HasPrefix(name, "calendar_") {
			t.Fatalf("native calendar tool %q is registered", name)
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
