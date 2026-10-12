package bootstrap

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/turns"
	"github.com/nigelteosw/eggy/internal/ports"
)

// codingToolNames are the tools that exist only inside a coding run. An owner
// turn must never be offered any of them.
var codingToolNames = []string{"list", "read", "search", "edit", "bash", "run_check", "ask_owner"}

func appWithAgentRunner(t *testing.T, enabled bool) *App {
	t.Helper()
	cfg := appTestConfig(t.TempDir())
	cfg.AgentRunner = config.AgentRunnerConfig{Enabled: enabled, Model: "deepseek-pro"}
	secrets := appTestSecrets("deepseek")
	secrets.EncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestAgentRunnerDisabledRegistersNothing(t *testing.T) {
	app := appWithAgentRunner(t, false)
	if app.agentRunner != nil {
		t.Fatal("a disabled agent runner built a service")
	}
	direct := app.loop.ToolNames(agent.RunOptions{})
	if slices.Contains(direct, "agent_runner") {
		t.Fatalf("disabled agent runner advertised its tool: %v", direct)
	}
	if app.commands.Code != nil {
		t.Fatal("/code is wired to a disabled agent runner")
	}
}

func TestAgentRunnerEnabledOffersStatusToolOnlyToOwnerTurns(t *testing.T) {
	app := appWithAgentRunner(t, true)
	if app.agentRunner == nil || app.commands.Code == nil {
		t.Fatal("enabled agent runner is not wired")
	}
	direct := app.loop.ToolNames(agent.RunOptions{})
	if !slices.Contains(direct, "agent_runner") {
		t.Fatalf("owner turns lack agent_runner: %v", direct)
	}
	for _, name := range codingToolNames {
		if slices.Contains(direct, name) {
			t.Fatalf("coding tool %q is in the owner catalog: %v", name, direct)
		}
	}
	scheduled := app.loop.ToolNames(turns.ReadOnlyTools())
	if slices.Contains(scheduled, "agent_runner") {
		t.Fatalf("unprompted turns can reach agent_runner: %v", scheduled)
	}
}

// Without a passing lock and probe, the runner refuses every command. This is
// the environment the tests run in: not root, so the data directory cannot be
// locked, and no command may run.
func TestAgentRunnerRefusesCommandsWhenUnlocked(t *testing.T) {
	app := appWithAgentRunner(t, true)
	ok, reason := app.agentRunner.SandboxStatus()
	if ok || reason == "" {
		t.Fatalf("sandbox status = %v %q; want unverified with a reason", ok, reason)
	}
}

// A /code run started from a chat must report back to that chat, and the
// origin must come from the verified ingress, not from the caller's text.
func TestCodeAdapterRecordsVerifiedOrigin(t *testing.T) {
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Telegram})
	origin := destination.FromContext(ctx)
	raw, err := json.Marshal(origin)
	if err != nil {
		t.Fatal(err)
	}
	var back destination.Destination
	if err := json.Unmarshal(raw, &back); err != nil || back.Kind != destination.Telegram {
		t.Fatalf("origin round trip = %+v err=%v", back, err)
	}
	if err := deliverToOrigin(ctx, nil, ports.AgentRun{OriginDestination: string(raw)}, "x"); err == nil {
		t.Fatal("a run with an origin but no channel reported success")
	}
}

// A run from the panel has no chat origin. An empty origin kind must never be
// sent anywhere, because routing an empty kind lands on the default channel.
func TestPanelRunHasNoChatOriginAndNoDelivery(t *testing.T) {
	if err := deliverToOrigin(context.Background(), nil, ports.AgentRun{OriginDestination: `{"kind":""}`}, "x"); err != nil {
		t.Fatalf("an empty origin kind was delivered: %v", err)
	}
	if err := deliverToOrigin(context.Background(), nil, ports.AgentRun{}, "x"); err != nil {
		t.Fatalf("a run with no origin was delivered: %v", err)
	}
}
