package services

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Shipper runs the commit -> push -> pull-request chain unattended and
// returns the resulting pull request, or a non-empty note when the chain
// stopped short (an unavailable capability or a protected branch).
type Shipper interface {
	Ship(ctx context.Context, runID, branch, commitMessage string) (ports.PullRequest, string, error)
}

type repositoryTool struct {
	definition ports.ToolDefinition
	execute    func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (t repositoryTool) Definition() ports.ToolDefinition { return t.definition }
func (t repositoryTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return t.execute(ctx, raw)
}

// NewRepositoryTools returns the repository tools that are not about one
// checkout: today that is repository_list alone. Changing a repository is no
// longer a tool that drives a run to completion -- it is workspace_open,
// workspace_edit, the primitives, and propose_change, in ordinary turns.
func NewRepositoryTools(store ports.StateStore) []ports.Tool {
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

	return []ports.Tool{list}
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
