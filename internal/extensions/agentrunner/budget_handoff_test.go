package agentrunner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// A cached prompt is re-read on every step of a coding loop. Counting it
// against the budget made the cap track the square of the number of steps, so
// a long but healthy run hit 2M tokens. The budget counts only new work.
func TestBudgetCountsNewWorkNotCachedPrompt(t *testing.T) {
	const steps = 100
	h := newHarness(t, nil, func(n int, _ ports.ModelRequest) (ports.ModelResponse, error) {
		if n > steps {
			return finalAnswer("done"), nil
		}
		return ports.ModelResponse{
			Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "c", Name: "list", Arguments: json.RawMessage(`{}`)}}},
			Usage:   ports.ModelUsage{PromptTokens: 30000, CachedPromptTokens: 29000, CompletionTokens: 500},
		}, nil
	})
	run := prepared(t, h, startRun(t, h, "a long but healthy change"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	got := stored(t, h, run.ID)
	if got.State != ports.AgentRunCompleted {
		t.Fatalf("run = state %s failure %q; cached prompt tokens should not spend the budget", got.State, got.Failure)
	}
	// 1,500 new tokens a step (1,000 prompt + 500 completion) over 100 steps.
	if want := int64(steps * 1500); got.UsedTokens != want {
		t.Fatalf("used tokens = %d, want %d", got.UsedTokens, want)
	}
}

// A provider that reports only a total still spends the budget by that total.
func TestNewWorkTokensFallsBackToTotal(t *testing.T) {
	if got := newWorkTokens(ports.ModelUsage{TotalTokens: 1234}); got != 1234 {
		t.Fatalf("newWorkTokens = %d, want the reported total", got)
	}
	if got := newWorkTokens(ports.ModelUsage{PromptTokens: 100, CachedPromptTokens: 150, CompletionTokens: 7}); got != 7 {
		t.Fatalf("newWorkTokens = %d, want completion only when everything was cached", got)
	}
}

// Every model call leaves a step in the run record with the tokens it spent,
// so the run page can show where the budget went.
func TestModelCallsRecordTheirSpend(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) {
		return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"},
			Usage: ports.ModelUsage{PromptTokens: 900, CachedPromptTokens: 600, CompletionTokens: 50}}, nil
	})
	run := prepared(t, h, startRun(t, h, "x"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	events, err := h.store.ListAgentRunEvents(ownerContext(context.Background(), run), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "model" && strings.Contains(event.Text, "350 new tokens") {
			return
		}
	}
	t.Fatalf("no model step recorded the spend: %+v", events)
}

// When a turn asks the owner something, its work so far travels with it, so
// the resumed turn does not explore the same files again.
func TestResumedRunCarriesTheFirstLegsFindings(t *testing.T) {
	h := newHarness(t, nil, func(n int, req ports.ModelRequest) (ports.ModelResponse, error) {
		switch n {
		case 1:
			return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "r", Name: "read", Arguments: json.RawMessage(`{"path":"README.md"}`)}}}}, nil
		case 2:
			return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "q", Name: "ask_owner", Arguments: json.RawMessage(`{"question":"Which file?"}`)}}}}, nil
		default:
			return finalAnswer("done"), nil
		}
	})
	run := prepared(t, h, startRun(t, h, "fix the docs"))
	if err := h.svc.execute(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Answer(acct("a"), run.ID, "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.execute(context.Background(), stored(t, h, run.ID)); err != nil {
		t.Fatal(err)
	}
	last := h.model.requests[len(h.model.requests)-1]
	brief := last.Messages[len(last.Messages)-1].Content
	if !strings.Contains(brief, "hello") {
		t.Fatalf("resumed turn does not carry the first leg's read:\n%s", brief)
	}
	if !strings.Contains(brief, "Which file?") || !strings.Contains(brief, "README.md") {
		t.Fatalf("question and answer missing from the resumed turn:\n%s", brief)
	}
}

// The coding run is given the coding context policy, which keeps excerpts
// exact enough to edit from.
func TestCodingPolicyPreservesExcerpts(t *testing.T) {
	policy := codingPolicy()
	if !policy.PreserveExcerpts || policy.FoldChars <= 0 || policy.FoldChars >= policy.RequestChars {
		t.Fatalf("coding policy = %+v", policy)
	}
}
