package commands

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/internal/storage/sqlite"
)

func TestMenuSettingsUseAccountRuntime(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runtime := services.NewAgentRuntime(db.State(), "fast", []string{"fast", "plain"}, map[string][]string{"fast": {"low", "high"}})
	svc := New(Options{AgentRuntime: runtime, ModelAliases: runtime.Aliases()})
	alice := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "alice"})
	bob := ports.WithPrincipal(t.Context(), ports.Principal{AccountID: "bob"})
	if err := runtime.SetShowThinking(alice, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetShowThinking(bob, false); err != nil {
		t.Fatal(err)
	}
	panel, err := svc.Control(alice, "settings")
	if err != nil || len(panel.Options) != 5 {
		t.Fatalf("panel=%+v err=%v", panel, err)
	}
	if _, err := svc.Control(alice, "set:/model settings thinking on"); err != nil {
		t.Fatal(err)
	}
	a, _ := runtime.ShowThinking(alice)
	b, _ := runtime.ShowThinking(bob)
	if !a || b {
		t.Fatalf("alice=%v bob=%v", a, b)
	}
	panel, err = svc.Control(alice, "menu")
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range panel.Options {
		if strings.Contains(option.Action, "code") || strings.Contains(option.Action, "runs") {
			t.Fatalf("deferred action: %+v", option)
		}
	}
	if _, err := svc.Control(alice, "set:/restart"); err == nil {
		t.Fatal("arbitrary command accepted")
	}
	if _, err := svc.Control(alice, "set:/mode auto"); err == nil {
		t.Fatal("auto bypassed explicit warning screen")
	}
}

func TestModelControlsPaginateLargeCatalog(t *testing.T) {
	aliases := make([]string, 25)
	for i := range aliases {
		aliases[i] = fmt.Sprintf("model-%02d", i)
	}
	svc := New(Options{AgentRuntime: &fakeModels{selected: aliases[0]}, ModelAliases: aliases})
	panel, err := svc.Control(t.Context(), "models")
	if err != nil || len(panel.Options) != 11 {
		t.Fatalf("panel=%+v err=%v", panel, err)
	}
	next := panel.Options[8]
	if next.Action != "models:8" {
		t.Fatalf("next=%+v", next)
	}
	panel, err = svc.Control(t.Context(), next.Action)
	if err != nil || panel.Options[0].Label != "model-08" {
		t.Fatalf("panel=%+v err=%v", panel, err)
	}
}
