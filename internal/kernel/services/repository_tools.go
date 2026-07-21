package services

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nigelteosw/eggy/internal/ports"
)

type RepositoryModifier interface {
	Start(context.Context, string, ports.Repository, string, func(ports.CodingProgress)) (ports.ImplementationSession, ports.CodingResult, error)
}

type RepositoryResumer interface {
	Resume(context.Context, string, string, func(ports.CodingProgress)) (ports.ImplementationSession, ports.CodingResult, error)
}

// Shipper runs the commit -> push -> pull-request chain unattended and
// returns the resulting pull request, or a non-empty note when the chain
// stopped short (an unavailable capability or a protected branch).
type Shipper interface {
	Ship(ctx context.Context, runID, branch, commitMessage string) (ports.PullRequest, string, error)
}

// RunCleaner destroys a completed run's temporary workspace.
type RunCleaner interface {
	Cleanup(context.Context, string) error
}

type repositoryTool struct {
	definition ports.ToolDefinition
	execute    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (t repositoryTool) Definition() ports.ToolDefinition { return t.definition }
func (t repositoryTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return t.execute(ctx, raw)
}

func NewRepositoryTools(
	store ports.StateStore,
	modifier RepositoryModifier,
	shipper Shipper,
	newRunID func() string,
	progress func(ports.CodingProgress),
) []ports.Tool {
	list := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_list", Description: "List repositories actually configured at runtime; never infer repository configuration from memory", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}}
	list.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if err := decodeStrict(raw, &struct{}{}); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		if len(registered) == 0 {
			return json.Marshal(map[string]any{"status": "not_configured", "repositories": []any{}, "message": "No repositories are configured. Configure repositories in Eggy's persisted configuration; do not send credentials in chat."})
		}
		type safeRepository struct {
			Name              string   `json:"name"`
			BaseBranch        string   `json:"base_branch"`
			ProtectedBranches []string `json:"protected_branches"`
		}
		names := make([]string, 0, len(registered))
		for name := range registered {
			names = append(names, name)
		}
		sort.Strings(names)
		result := make([]safeRepository, 0, len(names))
		for _, name := range names {
			repository := registered[name]
			result = append(result, safeRepository{Name: repository.Name, BaseBranch: repository.BaseBranch, ProtectedBranches: append([]string(nil), repository.ProtectedBranches...)})
		}
		return json.Marshal(map[string]any{"status": "configured", "repositories": result})
	}

	modify := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_modify", Description: "Use only for an explicit owner request to change a configured repository; runs the bounded implementation loop, then automatically commits, pushes, and opens a pull request without further owner approval. Report the pull-request URL from the result; never tell the owner to recover the temporary workspace manually", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["repository","instruction"],"additionalProperties":false}`),
	}}
	modify.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository  string `json:"repository"`
			Instruction string `json:"instruction"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		repository, err := lookupRepository(ctx, store, input.Repository)
		if err != nil {
			return nil, err
		}
		if modifier == nil || shipper == nil || newRunID == nil {
			return nil, errors.New("repository modification is unavailable")
		}
		runID := newRunID()
		trackedProgress := progress
		if progress != nil {
			trackedProgress = func(event ports.CodingProgress) {
				event.RunID = runID
				progress(event)
			}
		}
		run, result, err := modifier.Start(ctx, runID, repository, input.Instruction, trackedProgress)
		if err != nil {
			return nil, err
		}
		return shipResult(ctx, shipper, modifier, run, result)
	}

	resume := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_continue", Description: "Use only when the owner explicitly asks to continue or resume a named Eggy coding run. It resumes the durable workspace, then automatically commits and pushes without further owner approval. If the run's branch already has an open pull request, it reuses and updates that same pull request instead of opening a new one; only a branch with no open pull request yet gets one created.", Schema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["run_id","instruction"],"additionalProperties":false}`),
	}}
	resume.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			RunID       string `json:"run_id"`
			Instruction string `json:"instruction"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		resumer, ok := modifier.(RepositoryResumer)
		if !ok || shipper == nil {
			return nil, errors.New("repository continuation is unavailable")
		}
		trackedProgress := progress
		if progress != nil {
			trackedProgress = func(event ports.CodingProgress) {
				event.RunID = input.RunID
				progress(event)
			}
		}
		run, result, err := resumer.Resume(ctx, input.RunID, input.Instruction, trackedProgress)
		if err != nil {
			return nil, err
		}
		return shipResult(ctx, shipper, modifier, run, result)
	}
	return []ports.Tool{list, modify, resume}
}

// shipResult runs the automatic commit/push/pull-request chain for a
// completed implementation run and formats the tool result the model reports
// back to the owner.
func shipResult(ctx context.Context, shipper Shipper, modifier RepositoryModifier, run ports.ImplementationSession, result ports.CodingResult) (json.RawMessage, error) {
	pr, note, err := shipper.Ship(ctx, run.ID, run.Branch, result.CommitMessage)
	if err != nil {
		return nil, err
	}
	if note != "" {
		return json.Marshal(map[string]any{
			"status": "partial", "run_id": run.ID, "branch": run.Branch,
			"summary": result.Summary, "validation": result.Validation, "changed_files": result.ChangedFiles,
			"note": note,
		})
	}
	if cleaner, ok := modifier.(RunCleaner); ok {
		_ = cleaner.Cleanup(ctx, run.ID)
	}
	return json.Marshal(map[string]any{
		"status": "shipped", "run_id": run.ID, "branch": run.Branch,
		"summary": result.Summary, "validation": result.Validation, "changed_files": result.ChangedFiles,
		"pull_request_url": pr.URL, "pull_request_number": pr.Number,
	})
}

func loadRepositories(ctx context.Context, store ports.StateStore) (map[string]ports.Repository, error) {
	if store == nil {
		return nil, nil
	}
	state, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Repositories, nil
}
