package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	maxTreeEntries   = 200
	maxSearchMatches = 50
)

// NewRepositoryReadTools registers narrow, provider-neutral read-only repository
// tools. Each clones into an ephemeral workspace and never launches a coding
// agent, creates a branch, or leaves a diff.
func NewRepositoryReadTools(store ports.StateStore, runner ports.Runner, checkout ports.RepositoryCheckout, reader ports.RepositoryReader, newRunID func() string) []ports.Tool {
	withWorkspace := func(ctx context.Context, repositoryName string, use func(workspace string) (json.RawMessage, error)) (json.RawMessage, error) {
		repository, err := lookupRepository(ctx, store, repositoryName)
		if err != nil {
			return nil, err
		}
		if runner == nil || checkout == nil || reader == nil || newRunID == nil {
			return nil, errors.New("repository reading is unavailable")
		}
		workspace, err := runner.Create(ctx, "read-"+newRunID())
		if err != nil {
			return nil, err
		}
		defer runner.Destroy(context.Background(), workspace)
		if err := checkout.Clone(ctx, repository, workspace); err != nil {
			return nil, err
		}
		return use(workspace)
	}

	tree := repositoryTool{definition: ports.ToolDefinition{
		Name:        "repository_tree",
		Description: "List a bounded directory tree in a read-only checkout; creates no branch, commit, or approval",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"path":{"type":"string"}},"required":["repository"],"additionalProperties":false}`),
	}}
	tree.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			entries, err := reader.ListTree(ctx, workspace, input.Path, maxTreeEntries)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"repository": input.Repository, "entries": entries})
		})
	}

	search := repositoryTool{definition: ports.ToolDefinition{
		Name:        "repository_search",
		Description: "Search file names and text content in a read-only checkout; creates no branch, commit, or approval",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"query":{"type":"string","minLength":1}},"required":["repository","query"],"additionalProperties":false}`),
	}}
	search.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Query      string `json:"query"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			matches, err := reader.Search(ctx, workspace, input.Query, maxSearchMatches)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"repository": input.Repository, "matches": matches})
		})
	}

	read := repositoryTool{definition: ports.ToolDefinition{
		Name:        "repository_read",
		Description: "Read a bounded range of lines from a file in a read-only checkout; creates no branch, commit, or approval",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["repository","path"],"additionalProperties":false}`),
	}}
	read.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
			StartLine  int    `json:"start_line"`
			EndLine    int    `json:"end_line"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			content, err := reader.ReadFile(ctx, workspace, input.Path, input.StartLine, input.EndLine)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"repository": input.Repository, "path": input.Path, "content": content})
		})
	}

	status := repositoryTool{definition: ports.ToolDefinition{
		Name:        "repository_status",
		Description: "Report git status and branches for a read-only checkout without modifying anything",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1}},"required":["repository"],"additionalProperties":false}`),
	}}
	status.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			status, err := reader.Status(ctx, workspace)
			if err != nil {
				return nil, err
			}
			branches, err := reader.Branches(ctx, workspace)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"repository": input.Repository, "status": status, "branches": branches})
		})
	}

	metadata := repositoryTool{definition: ports.ToolDefinition{
		Name:        "repository_github",
		Description: "Read GitHub repository, issue, pull-request, or check-run metadata; never mutates GitHub state",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"kind":{"type":"string","enum":["repository","issue","pull_request","checks"]},"number":{"type":"integer","minimum":1},"ref":{"type":"string"}},"required":["repository","kind"],"additionalProperties":false}`),
	}}
	metadata.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Kind       string `json:"kind"`
			Number     int    `json:"number"`
			Ref        string `json:"ref"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		repository, err := lookupRepository(ctx, store, input.Repository)
		if err != nil {
			return nil, err
		}
		if reader == nil {
			return nil, errors.New("repository reading is unavailable")
		}
		switch input.Kind {
		case "repository":
			summary, err := reader.RepositorySummary(ctx, repository)
			if err != nil {
				return nil, err
			}
			return json.Marshal(summary)
		case "issue":
			if input.Number <= 0 {
				return nil, errors.New(`number is required for kind "issue"`)
			}
			summary, err := reader.Issue(ctx, repository, input.Number)
			if err != nil {
				return nil, err
			}
			return json.Marshal(summary)
		case "pull_request":
			if input.Number <= 0 {
				return nil, errors.New(`number is required for kind "pull_request"`)
			}
			summary, err := reader.PullRequestSummary(ctx, repository, input.Number)
			if err != nil {
				return nil, err
			}
			return json.Marshal(summary)
		case "checks":
			if input.Ref == "" {
				return nil, errors.New(`ref is required for kind "checks"`)
			}
			checks, err := reader.Checks(ctx, repository, input.Ref)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"repository": input.Repository, "checks": checks})
		default:
			return nil, fmt.Errorf("unsupported kind %q", input.Kind)
		}
	}

	return []ports.Tool{tree, search, read, status, metadata}
}

func lookupRepository(ctx context.Context, store ports.StateStore, name string) (ports.Repository, error) {
	registered, err := loadRepositories(ctx, store)
	if err != nil {
		return ports.Repository{}, err
	}
	repository, ok := registered[name]
	if !ok {
		return ports.Repository{}, fmt.Errorf("repository %q is not configured", name)
	}
	return repository, nil
}
