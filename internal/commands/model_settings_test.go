package commands

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

func TestModelSettingsUseRuntimeValidationAndAccountState(t *testing.T) {
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	runtime := services.NewAgentRuntime(database.State(), "fast", []string{"fast", "plain"}, map[string][]string{"fast": {"low", "high"}})
	service := New(Options{AgentRuntime: runtime, DefaultModel: "fast", ModelAliases: runtime.Aliases()})
	alice := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "alice"})
	bob := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "bob"})
	execute := func(ctx context.Context, command string) string {
		t.Helper()
		output, handled, err := service.Execute(ctx, command)
		if err != nil || !handled {
			t.Fatalf("%s: %s %v", command, output, err)
		}
		return output
	}
	for _, fragment := range []string{"fast", "default"} {
		if output := execute(alice, "/model"); !strings.Contains(output, fragment) {
			t.Fatalf("model report=%s", output)
		}
	}
	if output := execute(alice, "/model effort"); !strings.Contains(output, "low") || !strings.Contains(output, "high") {
		t.Fatalf("effort report=%s", output)
	}
	execute(alice, "/model effort high")
	if effort, _ := runtime.ReasoningEffort(alice); effort != "high" {
		t.Fatalf("effort=%q", effort)
	}
	execute(alice, "/model effort impossible")
	if effort, _ := runtime.ReasoningEffort(alice); effort != "high" {
		t.Fatal("invalid effort changed state")
	}
	execute(alice, "/model thinking off")
	if show, _ := runtime.ShowThinking(alice); show {
		t.Fatal("thinking not hidden")
	}
	execute(alice, "/model thinking nonsense")
	if show, _ := runtime.ShowThinking(alice); show {
		t.Fatal("invalid thinking changed state")
	}
	if effort, _ := runtime.ReasoningEffort(bob); effort != "" {
		t.Fatal("effort leaked to bob")
	}
	if show, _ := runtime.ShowThinking(bob); !show {
		t.Fatal("thinking leaked to bob")
	}
	execute(alice, "/model plain")
	if effort, _ := runtime.ReasoningEffort(alice); effort != "" {
		t.Fatal("unsupported stored effort applied")
	}
	execute(alice, "/model effort default")
	execute(alice, "/model fast")
	if effort, _ := runtime.ReasoningEffort(alice); effort != "" {
		t.Fatal("default reset did not clear stored effort")
	}
	execute(alice, "/model effort low")
	execute(alice, "/model effort default")
	if effort, _ := runtime.ReasoningEffort(alice); effort != "" {
		t.Fatal("default reset failed on reasoning model")
	}
}

func TestModelSettingAliasesRemainSelectable(t *testing.T) {
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	runtime := services.NewAgentRuntime(database.State(), "effort", []string{"effort", "thinking", "settings"}, map[string][]string{"effort": {"high"}, "thinking": {"high"}, "settings": {"high"}})
	service := New(Options{AgentRuntime: runtime, ModelAliases: runtime.Aliases()})
	ctx := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "alice"})
	for _, alias := range runtime.Aliases() {
		if _, _, err := service.Execute(ctx, "/model "+alias); err != nil {
			t.Fatal(err)
		}
		if selected, _ := runtime.SelectedModel(ctx); selected != alias {
			t.Fatalf("alias %s shadowed by settings", alias)
		}
		service.Execute(ctx, "/model settings effort high")
		if effort, _ := runtime.ReasoningEffort(ctx); effort != "high" {
			t.Fatalf("explicit effort unavailable for alias %s", alias)
		}
		service.Execute(ctx, "/model settings thinking off")
		if show, _ := runtime.ShowThinking(ctx); show {
			t.Fatalf("explicit thinking unavailable for alias %s", alias)
		}
	}
}
