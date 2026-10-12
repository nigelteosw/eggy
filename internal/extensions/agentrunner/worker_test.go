package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// prepared readies a queued run for the worker: the base SHA is already known,
// so no clone runs, and the workspace exists.
func prepared(t *testing.T, h *harness, run ports.AgentRun) ports.AgentRun {
	t.Helper()
	run.BaseSHA = "base0000"
	updated, err := h.store.UpdateAgentRun(acct("a"), run)
	if err != nil {
		t.Fatal(err)
	}
	if err := makeRunDirs(h.svc.opts.Workspaces.Dir(run.ID)); err != nil {
		t.Fatal(err)
	}
	return updated
}

func stored(t *testing.T, h *harness, id string) ports.AgentRun {
	t.Helper()
	run, err := h.store.GetAgentRun(acct("a"), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func toolNames(defs []ports.ToolDefinition) []string {
	var names []string
	for _, d := range defs {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	return names
}

func TestCodingTurnSeesOnlyCodingTools(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) { return finalAnswer("done"), nil })
	run := prepared(t, h, startRun(t, h, "change it"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got := toolNames(h.model.requests[0].Tools)
	want := []string{"ask_owner", "bash", "edit", "list", "read", "run_check", "search"}
	if !slices.Equal(got, want) {
		t.Fatalf("offered tools = %v, want %v", got, want)
	}
}

func TestCodingTurnUsesRunnerModelAndNoBashWithoutSandbox(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.SandboxOK = false }, func(int, ports.ModelRequest) (ports.ModelResponse, error) {
		return finalAnswer("done"), nil
	})
	run := prepared(t, h, startRun(t, h, "change it"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	req := h.model.requests[0]
	if req.Model != "fake-model" {
		t.Fatalf("model id = %q", req.Model)
	}
	if slices.Contains(toolNames(req.Tools), "bash") {
		t.Fatal("bash offered without a passing probe")
	}
}

func TestDisabledSkillNotInPrompt(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Skills = map[string]bool{"brainstorming": false, "writing-plans": true} }, func(int, ports.ModelRequest) (ports.ModelResponse, error) {
		return finalAnswer("done"), nil
	})
	run := prepared(t, h, startRun(t, h, "x"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	prompt := h.model.requests[0].Messages[0].Content
	if strings.Contains(prompt, "## Skill: brainstorming") {
		t.Fatal("disabled skill reached the prompt")
	}
	if !strings.Contains(prompt, "## Skill: writing-plans") {
		t.Fatal("enabled skill missing from the prompt")
	}
}

func TestBudgetTokensFailsRun(t *testing.T) {
	h := newHarness(t, nil, func(n int, _ ports.ModelRequest) (ports.ModelResponse, error) {
		return ports.ModelResponse{
			Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "c", Name: "list", Arguments: json.RawMessage(`{}`)}}},
			Usage:   ports.ModelUsage{TotalTokens: 1_000_000},
		}, nil
	})
	run := prepared(t, h, startRun(t, h, "loop forever"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got := stored(t, h, run.ID)
	if got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "token budget") {
		t.Fatalf("run = state %s failure %q", got.State, got.Failure)
	}
	if got.UsedTokens < maxRunTokens {
		t.Fatalf("used tokens = %d, want at least the cap", got.UsedTokens)
	}
}

func TestBudgetWallTimeFailsRun(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) { return finalAnswer("x"), nil })
	run := startRun(t, h, "x")
	run.BaseSHA = "base0000"
	run.UsedActive = maxRunActive
	run, _ = h.store.UpdateAgentRun(acct("a"), run)
	_ = makeRunDirs(h.svc.opts.Workspaces.Dir(run.ID))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, h, run.ID); got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "wall-time") {
		t.Fatalf("run = %+v", got)
	}
}

func TestAskOwnerReleasesSlotAndAnswerRequeues(t *testing.T) {
	h := newHarness(t, nil, func(n int, req ports.ModelRequest) (ports.ModelResponse, error) {
		if n == 1 {
			return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "q", Name: "ask_owner", Arguments: json.RawMessage(`{"question":"Which file?"}`)}}}}, nil
		}
		// After the answer, the brief the model sees includes it.
		last := req.Messages[len(req.Messages)-1].Content
		if !strings.Contains(last, "README.md") {
			return ports.ModelResponse{}, errors.New("answer missing from the turn")
		}
		return finalAnswer("updated README.md"), nil
	})
	run := prepared(t, h, startRun(t, h, "fix the docs"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	waiting := stored(t, h, run.ID)
	if waiting.State != ports.AgentRunWaitingForUser || waiting.Question != "Which file?" {
		t.Fatalf("after question: %+v", waiting)
	}
	if _, err := h.svc.Answer(acct("a"), run.ID, "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.execute(context.Background(), stored(t, h, run.ID)); err != nil {
		t.Fatal(err)
	}
	done := stored(t, h, run.ID)
	if done.State != ports.AgentRunCompleted {
		t.Fatalf("after answer: state %s failure %q", done.State, done.Failure)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notes) == 0 || !strings.Contains(h.notes[0], "Which file?") {
		t.Fatalf("owner was not notified of the question: %v", h.notes)
	}
}

func TestWaitingRunExpiresWithMandate(t *testing.T) {
	h := newHarness(t, nil, nil)
	run := startRun(t, h, "x")
	run.State = ports.AgentRunWaitingForUser
	run.MandateExpires = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	run, _ = h.store.UpdateAgentRun(acct("a"), run)
	if err := h.svc.expireWaiting(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, h, run.ID); got.State != ports.AgentRunCancelled {
		t.Fatalf("state = %s, want cancelled", got.State)
	}
}

func TestRepositoryRemovedWhileQueuedFails(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) { return finalAnswer("x"), nil })
	run := prepared(t, h, startRun(t, h, "x"))
	h.svc.opts.Repositories = func() []config.RepositoryConfig { return nil }
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, h, run.ID); got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "no longer allowed") {
		t.Fatalf("run = %+v", got)
	}
}

func TestStartupMarksRunningInterrupted(t *testing.T) {
	h := newHarness(t, nil, nil)
	run := startRun(t, h, "x")
	run.State = ports.AgentRunRunning
	run, _ = h.store.UpdateAgentRun(acct("a"), run)
	if err := h.svc.reconcileStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, h, run.ID); got.State != ports.AgentRunInterrupted {
		t.Fatalf("state = %s, want interrupted", got.State)
	}
}

func TestCompletedRunDeliversSummary(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) { return finalAnswer("changed README"), nil })
	run := prepared(t, h, startRun(t, h, "x"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got := stored(t, h, run.ID)
	if got.State != ports.AgentRunCompleted || got.Output != "changed README" {
		t.Fatalf("run = %+v", got)
	}
}
