package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

// errTokenBudget is returned by the budget wrapper once a run has spent its
// token ceiling. The loop hands it back as the turn's error.
var errTokenBudget = errors.New("token budget exhausted")

// tokenBudget counts the tokens one run has spent, across every turn of it.
type tokenBudget struct {
	used int64
}

// budgetModel enforces the token ceiling on each model call, so a turn that
// never stops calling tools ends at the cap instead of running on.
type budgetModel struct {
	inner  ports.Model
	budget *tokenBudget
}

func (b budgetModel) Generate(ctx context.Context, req ports.ModelRequest) (ports.ModelResponse, error) {
	if b.budget.used >= maxRunTokens {
		return ports.ModelResponse{}, fmt.Errorf("%w (%d tokens)", errTokenBudget, maxRunTokens)
	}
	resp, err := b.inner.Generate(ctx, req)
	total := resp.Usage.TotalTokens
	if total == 0 {
		total = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}
	b.budget.used += total
	return resp, err
}

// codingPolicy is the context budget for a coding turn. A long turn compacts
// and continues; MaxSteps is the one hard step limit.
func codingPolicy() agent.ContextPolicy {
	return agent.ContextPolicy{
		BudgetChars:        96000,
		RecentSteps:        16,
		OutputExcerptChars: 8192,
		RequestChars:       320000,
		ReserveChars:       16000,
		MaxSteps:           maxCodingSteps,
	}
}

// Run is the worker. It serves every account, one run at a time, so the
// deployment has one active run. It returns when ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if err := s.reconcileStartup(ctx); err != nil {
		s.log().Error("agent runner: startup reconciliation", "error", err)
	}
	if err := s.reconcile(ctx); err != nil {
		s.log().Error("agent runner: publication reconciliation", "error", err)
	}
	if err := s.sweep(ctx); err != nil {
		s.log().Error("agent runner: workspace sweep", "error", err)
	}
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for {
		if err := s.drain(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("agent runner: worker", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

// sweep applies the retention rules to run directories. A run still in flight
// keeps its workspace; a failed, cancelled or interrupted run keeps it for
// retainedFailure; a completed run's workspace goes once it is published. A
// directory with no run record is an orphan and goes.
func (s *Service) sweep(ctx context.Context) error {
	runs, err := s.opts.Store.AgentRunsInStates(ctx, ports.AgentRunQueued, ports.AgentRunRunning, ports.AgentRunWaitingForUser,
		ports.AgentRunPublishing, ports.AgentRunCompleted, ports.AgentRunFailed, ports.AgentRunCancelled, ports.AgentRunInterrupted)
	if err != nil {
		return err
	}
	byID := make(map[string]ports.AgentRun, len(runs))
	for _, run := range runs {
		byID[run.ID] = run
	}
	now := s.opts.Now()
	return s.opts.Workspaces.Sweep(now, func(id string) (time.Time, bool) {
		run, ok := byID[id]
		if !ok {
			return time.Time{}, false
		}
		switch run.State {
		case ports.AgentRunFailed, ports.AgentRunCancelled, ports.AgentRunInterrupted:
			return run.UpdatedAt.Add(retainedFailure), true
		case ports.AgentRunCompleted:
			return time.Time{}, false
		default:
			// In flight: kept until the run ends and the sweep runs again.
			return now.Add(maxRunActive), true
		}
	})
}

// drain runs queued work until none is left.
func (s *Service) drain(ctx context.Context) error {
	if err := s.expireWaiting(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		run, ok, err := s.opts.Store.NextQueuedAgentRun(ctx)
		if err != nil || !ok {
			return err
		}
		if err := s.execute(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) log() *slog.Logger {
	return slog.Default().With("component", "agent_runner")
}

// reconcileStartup records a coding run a restart cut off. It is not relaunched:
// a coding process cannot be resumed reliably, and the owner decides whether to
// start again. A run in publication is not touched here; reconcile resumes it.
func (s *Service) reconcileStartup(ctx context.Context) error {
	running, err := s.opts.Store.AgentRunsInStates(ctx, ports.AgentRunRunning)
	if err != nil {
		return err
	}
	for _, run := range running {
		run.State = ports.AgentRunInterrupted
		run.Failure = "interrupted by a restart; start the run again to retry"
		if _, err := s.opts.Store.UpdateAgentRun(ownerContext(ctx, run), run); err != nil {
			return err
		}
	}
	return nil
}

// expireWaiting cancels a run that waited past its mandate for an answer. Its
// workspace is kept, so the owner can still inspect what was done.
func (s *Service) expireWaiting(ctx context.Context) error {
	waiting, err := s.opts.Store.AgentRunsInStates(ctx, ports.AgentRunWaitingForUser)
	if err != nil {
		return err
	}
	now := s.opts.Now()
	for _, run := range waiting {
		if now.Before(run.MandateExpires) {
			continue
		}
		run.State = ports.AgentRunCancelled
		run.Failure = "mandate expired while waiting for an answer"
		if _, err := s.opts.Store.UpdateAgentRun(ownerContext(ctx, run), run); err != nil {
			return err
		}
	}
	return nil
}

// ownerContext is the context a run's writes need: its stored owner as the
// principal. The worker serves every account, so the owner always comes from
// the run's own row, never from anything a caller supplied.
func ownerContext(ctx context.Context, run ports.AgentRun) context.Context {
	return ports.WithPrincipal(ctx, ports.Principal{AccountID: run.AccountID})
}

// execute runs one queued run: clone on first use, the coding turn, then the
// outcome. Every outcome is recorded on the run, so none is lost to a crash
// after the turn.
func (s *Service) execute(ctx context.Context, run ports.AgentRun) error {
	ctx = ownerContext(ctx, run)
	if !s.opts.Now().Before(run.MandateExpires) {
		return s.finish(ctx, run, ports.AgentRunFailed, "the run's mandate expired before it started", "")
	}
	repo, ok := s.repository(run.Repository)
	if !ok {
		return s.finish(ctx, run, ports.AgentRunFailed, "repository no longer allowed", "")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[run.ID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, run.ID)
		s.mu.Unlock()
		cancel()
	}()

	run.State = ports.AgentRunRunning
	run, err := s.opts.Store.UpdateAgentRun(ctx, run)
	if err != nil {
		return err
	}
	s.note(ctx, run.ID, "started", "coding run started on "+run.Repository+" from "+run.BaseBranch)
	if run.BaseSHA == "" {
		token, generation, err := s.opts.Credentials.token(runCtx)
		if err != nil {
			return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("GitHub token unavailable: "+err.Error()), "")
		}
		if generation != run.PATGeneration {
			return s.finish(ctx, run, ports.AgentRunFailed, "GitHub token changed since the run started", "")
		}
		base, err := s.opts.Workspaces.Clone(runCtx, run.ID, repo.CloneURL, run.BaseBranch, token)
		if err != nil {
			return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("clone failed: "+err.Error()), "")
		}
		run.BaseSHA = base
		if run, err = s.opts.Store.UpdateAgentRun(ctx, run); err != nil {
			return err
		}
	}
	return s.codingTurn(ctx, runCtx, run, repo)
}

// codingTurn runs the model over the coding tools until it stops, asks the
// owner, or hits a limit, then records which.
func (s *Service) codingTurn(ctx, runCtx context.Context, run ports.AgentRun, repo config.RepositoryConfig) error {
	active := &ActiveRun{ID: run.ID, Checks: repo.Checks}
	tools := append(CodingTools(s.opts.Workspaces, active, s.opts.SandboxOK), codeTool{
		def: def("ask_owner", "Ask the owner one question you cannot resolve yourself. Then end your turn.",
			`{"type":"object","properties":{"question":{"type":"string"}},"required":["question"],"additionalProperties":false}`, ports.ToolEffect{}),
		run: func(_ context.Context, in json.RawMessage) (string, error) {
			var args struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal(in, &args); err != nil {
				return "", err
			}
			if len(args.Question) > maxAskQuestionLen {
				args.Question = args.Question[:maxAskQuestionLen]
			}
			active.setQuestion(args.Question)
			return "Question recorded. End your turn now and wait for the owner's answer.", nil
		},
	})

	for i := range tools {
		tools[i] = recordedTool{Tool: tools[i], s: s, ctx: ctx, runID: run.ID}
	}

	budget := &tokenBudget{used: run.UsedTokens}
	target := s.opts.Models[s.opts.Model]
	target.Model = budgetModel{inner: target.Model, budget: budget}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{s.opts.Model: target}, agent.StaticTools(tools), codingPolicy())

	remaining := maxRunActive - run.UsedActive
	if remaining <= 0 {
		return s.finish(ctx, run, ports.AgentRunFailed, "wall-time limit reached", "")
	}
	turnCtx, cancelTurn := context.WithTimeout(runCtx, remaining)
	defer cancelTurn()
	started := s.opts.Now()
	result, err := loop.Run(turnCtx, s.opts.Model, "", s.turnInput(run), s.instructionMessages(), agent.RunOptions{})
	run.UsedActive += s.opts.Now().Sub(started)
	run.UsedTokens = budget.used

	switch {
	case ctx.Err() != nil:
		// The worker is shutting down. The write must outlive the cancelled
		// context, or the run would be left looking like it is still running.
		return s.finish(context.WithoutCancel(ctx), run, ports.AgentRunInterrupted, "interrupted by a restart; start the run again to retry", "")
	case runCtx.Err() != nil:
		return s.finish(ctx, run, ports.AgentRunCancelled, "cancelled by owner", "")
	case errors.Is(err, errTokenBudget):
		return s.finish(ctx, run, ports.AgentRunFailed, fmt.Sprintf("token budget exhausted (%d tokens)", maxRunTokens), "")
	case turnCtx.Err() != nil:
		return s.finish(ctx, run, ports.AgentRunFailed, "wall-time limit reached", "")
	case active.Question() != "":
		run.Question = active.Question()
		return s.waitFor(ctx, run)
	case err != nil:
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("coding turn: "+err.Error()), "")
	}
	return s.publish(ctx, run, repo, active.results(), result.Message.Content)
}

// turnInput is the brief, plus the owner's answer when the run is resuming.
func (s *Service) turnInput(run ports.AgentRun) ports.Message {
	text := run.Brief
	if run.Answer != "" {
		text += "\n\nYou asked the owner: " + run.Question + "\nTheir answer: " + run.Answer
	}
	return ports.Message{Role: ports.RoleUser, Content: text}
}

// waitFor parks a run on a question: it leaves the worker's slot, and the
// owner is told what to answer.
func (s *Service) waitFor(ctx context.Context, run ports.AgentRun) error {
	run.State = ports.AgentRunWaitingForUser
	run.Answer = ""
	updated, err := s.opts.Store.UpdateAgentRun(ctx, run)
	if err != nil {
		return err
	}
	s.note(ctx, run.ID, "question", run.Question)
	s.deliver(ctx, updated, "The agent runner has a question for you on "+updated.Repository+": "+updated.Question+"\nAnswer it from the Agent Runner page or with /code answer "+updated.ID+" <text>.")
	return nil
}

// finish records a terminal outcome. Publication is added by the publisher;
// until then a completed run carries its summary as its output.
func (s *Service) finish(ctx context.Context, run ports.AgentRun, state ports.AgentRunState, failure, output string) error {
	run.State = state
	run.Failure = failure
	if output != "" {
		run.Output = truncate(s.opts.Redact(output), maxOutputBytes)
	}
	updated, err := s.opts.Store.UpdateAgentRun(ctx, run)
	if err != nil {
		return err
	}
	if failure != "" {
		s.note(ctx, run.ID, string(state), failure)
	} else {
		s.note(ctx, run.ID, string(state), "finished")
	}
	if state == ports.AgentRunCompleted {
		s.deliver(ctx, updated, "Run finished on "+updated.Repository+": "+updated.Output)
	} else if state == ports.AgentRunFailed {
		s.deliver(ctx, updated, "Run failed on "+updated.Repository+": "+failure)
	}
	return nil
}

// deliver sends text to the channel the run started from. A failed delivery is
// logged; the run's state is already durable, so it is not undone.
func (s *Service) deliver(ctx context.Context, run ports.AgentRun, text string) {
	if s.opts.Notify == nil {
		return
	}
	if err := s.opts.Notify(ctx, run, text); err != nil {
		s.log().Error("agent runner: notify", "run", run.ID, "error", err)
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

// maxEventText bounds one step in the run record. A check's output can run
// long; the record is for reading what happened, not for replaying it.
const maxEventText = 500

// note records one step of a run. It is best effort: a failed write is logged
// and never fails the run the step describes, because the step is a record of
// the run, not part of its work.
func (s *Service) note(ctx context.Context, runID, kind, text string) {
	text = truncate(s.opts.Redact(strings.TrimSpace(text)), maxEventText)
	if err := s.opts.Store.AppendAgentRunEvent(ctx, runID, ports.AgentRunEvent{At: s.opts.Now(), Kind: kind, Text: text}); err != nil {
		slog.Warn("agent run step not recorded", "run_id", runID, "kind", kind, "error", err)
	}
}

// recordedTool wraps a tool so each call, and what it returned or failed with,
// is a step in the run record. The call's result is unchanged.
type recordedTool struct {
	ports.Tool
	s     *Service
	ctx   context.Context
	runID string
}

func (t recordedTool) Execute(callCtx context.Context, input json.RawMessage) (json.RawMessage, error) {
	out, err := t.Tool.Execute(callCtx, input)
	outcome := toolOutcome(out)
	if err != nil {
		outcome = "error: " + err.Error()
	}
	t.s.note(t.ctx, t.runID, "tool", t.Tool.Definition().Name+": "+outcome)
	return out, err
}

// toolOutcome is the text a coding tool returned. Coding tools wrap their text
// as {"output": ...}; the record keeps the text the model read, not the wrapper.
func toolOutcome(out json.RawMessage) string {
	var wrapped struct {
		Output *string `json:"output"`
	}
	if json.Unmarshal(out, &wrapped) == nil && wrapped.Output != nil {
		return *wrapped.Output
	}
	return string(out)
}
