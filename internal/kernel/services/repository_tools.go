package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type RepositoryInspector interface {
	Inspect(context.Context, string, ports.Repository, string) (ports.CodingResult, error)
}

type RepositoryModifier interface {
	Start(context.Context, string, ports.Repository, string, func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error)
}

type CommitApprovalRequester interface {
	RequestCommit(context.Context, string, string) (approvals.Approval, error)
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
	inspector RepositoryInspector,
	modifier RepositoryModifier,
	approvalRequester CommitApprovalRequester,
	newRunID func() string,
	progress func(ports.CodingProgress),
	deliverApproval func(context.Context, approvals.Approval) error,
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

	inspect := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_inspect", Description: "Answer a read-only question using Codex in an isolated checkout; creates no branch or approval and must be used before claiming repository implementation facts", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"question":{"type":"string","minLength":1}},"required":["repository","question"],"additionalProperties":false}`),
	}}
	inspect.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Question   string `json:"question"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		repository, ok := registered[input.Repository]
		if !ok {
			return nil, fmt.Errorf("repository %q is not configured", input.Repository)
		}
		if inspector == nil || newRunID == nil {
			return nil, errors.New("repository inspection is unavailable")
		}
		result, err := inspector.Inspect(ctx, "inspect-"+newRunID(), repository, input.Question)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"repository": repository.Name, "summary": result.Summary, "validation": result.Validation})
	}

	modify := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_modify", Description: "Use only for an explicit owner request to change a configured repository; runs the coding adapter and requests commit approval. When provider capabilities are ready, Eggy automatically chains separate push and pull-request approvals; never tell the owner to recover the temporary workspace manually", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["repository","instruction"],"additionalProperties":false}`),
	}}
	modify.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository  string `json:"repository"`
			Instruction string `json:"instruction"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		registered, err := loadRepositories(ctx, store)
		if err != nil {
			return nil, err
		}
		repository, ok := registered[input.Repository]
		if !ok {
			return nil, fmt.Errorf("repository %q is not configured", input.Repository)
		}
		if modifier == nil || approvalRequester == nil || newRunID == nil {
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
		approval, err := approvalRequester.RequestCommit(ctx, run.ID, result.CommitMessage)
		if err != nil {
			return nil, err
		}
		if deliverApproval != nil {
			if err := deliverApproval(ctx, approval); err != nil {
				return nil, err
			}
		}
		return json.Marshal(map[string]any{
			"status": "awaiting_owner", "run_id": run.ID, "branch": run.Branch, "base_revision": run.BaseRevision, "approval_id": approval.ID,
			"summary": result.Summary, "validation": result.Validation, "changed_files": result.ChangedFiles,
			"commit_created": false, "next_action": "approve_commit", "approval_flow": "commit -> push -> pull_request",
		})
	}
	return []ports.Tool{list, inspect, modify}
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
