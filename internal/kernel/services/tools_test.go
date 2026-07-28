package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestStatusToolReturnsBoundedOperationalView(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	store.state.Approvals["a"] = approvals.Approval{ID: "a", Status: approvals.Pending}
	changeStore := newMemoryChangeStore()
	changeStore.changes["run-1"] = ports.Change{ID: "run-1", Phase: ports.PhaseRunning}
	changeStore.changes["run-2"] = ports.Change{ID: "run-2", Phase: ports.PhaseCompleted}
	changes := NewChanges(changeStore, time.Now)
	tool := NewStatusTool(store, changes, stubSchedules{{ID: "morning"}})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Repositories     []string `json:"repositories"`
		ActiveRuns       int      `json:"active_runs"`
		PendingApprovals int      `json:"pending_approvals"`
		Schedules        int      `json:"schedules"`
	}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.Schedules != 1 {
		t.Fatalf("schedules=%d, want the count from the cron directory", status.Schedules)
	}
	if len(status.Repositories) != 1 || status.Repositories[0] != "eggy" || status.PendingApprovals != 1 || status.ActiveRuns != 1 {
		t.Fatalf("status=%s", result)
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

// stubSchedules stands in for the cron-backed scheduler.
type stubSchedules []ports.Schedule

func (s stubSchedules) List(context.Context) ([]ports.Schedule, error) { return s, nil }

// registryTool is a minimal ports.Tool for registry composition tests.
type registryTool struct{ name string }

func (t registryTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (t registryTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func toolNames(tools []ports.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Definition().Name)
	}
	return names
}

func TestToolRegistryMergesProviderCatalogAfterRegisteredTools(t *testing.T) {
	registry := NewToolRegistry()
	for _, name := range []string{"terminal", "read_file"} {
		if err := registry.Register(registryTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	registry.AddProvider(func() []ports.Tool {
		return []ports.Tool{registryTool{name: "railway_deploy"}, registryTool{name: "aaa_remote"}}
	})
	got := toolNames(registry.Tools())
	want := []string{"read_file", "terminal", "aaa_remote", "railway_deploy"}
	if len(got) != len(want) {
		t.Fatalf("tools=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tools=%v, want %v (registered first, each group name-sorted)", got, want)
		}
	}
}

// The invariant that keeps the kernel primitives defined exactly once: a
// remote catalog advertising "terminal" must not displace the real one. This
// used to live in the loop; it belongs to whatever composes the catalog.
func TestToolRegistryProviderCannotShadowARegisteredTool(t *testing.T) {
	registry := NewToolRegistry()
	primitive := registryTool{name: "terminal"}
	if err := registry.Register(primitive); err != nil {
		t.Fatal(err)
	}
	registry.AddProvider(func() []ports.Tool {
		return []ports.Tool{registryTool{name: "terminal"}, registryTool{name: "safe"}}
	})
	tools := registry.Tools()
	if names := toolNames(tools); len(names) != 2 || names[0] != "terminal" || names[1] != "safe" {
		t.Fatalf("tools=%v, want the registered terminal plus safe", toolNames(tools))
	}
	if tools[0] != ports.Tool(primitive) {
		t.Fatal("provider tool displaced the registered primitive")
	}
}

// Two providers cannot shadow each other either: first registered wins, so
// the merge is deterministic rather than dependent on catalog timing.
func TestToolRegistryFirstProviderWinsBetweenProviders(t *testing.T) {
	registry := NewToolRegistry()
	first := registryTool{name: "shared"}
	registry.AddProvider(func() []ports.Tool { return []ports.Tool{first} })
	registry.AddProvider(func() []ports.Tool { return []ports.Tool{registryTool{name: "shared"}} })
	tools := registry.Tools()
	if len(tools) != 1 || tools[0] != ports.Tool(first) {
		t.Fatalf("tools=%v, want only the first provider's tool", toolNames(tools))
	}
}

// The catalog is read on every call, so a server that reconnects or is logged
// out of changes what the next turn sees without rebuilding anything.
func TestToolRegistryReadsProvidersOnEveryCall(t *testing.T) {
	registry := NewToolRegistry()
	catalog := []ports.Tool{registryTool{name: "remote"}}
	registry.AddProvider(func() []ports.Tool { return catalog })
	if names := toolNames(registry.Tools()); len(names) != 1 {
		t.Fatalf("tools=%v", names)
	}
	catalog = nil
	if names := toolNames(registry.Tools()); len(names) != 0 {
		t.Fatalf("tools=%v, want an emptied catalog to be visible immediately", names)
	}
}
