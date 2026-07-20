package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
)

// NewRepositoryReadTools registers narrow, provider-neutral read-only
// repository tools. read_file and terminal each clone into an ephemeral
// workspace and never launch an implementation run, create a branch, or
// leave a diff; repository_github never clones at all.
func NewRepositoryReadTools(store ports.StateStore, runner ports.Runner, checkout ports.RepositoryCheckout, reader ports.RepositoryReader, newRunID func() string) []ports.Tool {
	withEphemeralWorkspace := func(ctx context.Context, repositoryName string, use func(workspace string) (json.RawMessage, error)) (json.RawMessage, error) {
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

	readFile := repositoryTool{definition: ports.ToolDefinition{
		Name:        "read_file",
		Description: "Read a bounded range of lines from a file in a read-only checkout; creates no branch, commit, or approval",
		Schema:      json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["repository","path"],"additionalProperties":false}`),
	}}
	readFile.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Path       string `json:"path"`
			StartLine  int    `json:"start_line"`
			EndLine    int    `json:"end_line"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withEphemeralWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			content, err := reader.ReadFile(ctx, workspace, input.Path, input.StartLine, input.EndLine)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"repository": input.Repository, "path": input.Path, "content": content})
		})
	}

	terminal := repositoryTool{definition: ports.ToolDefinition{
		Name:        "terminal",
		Description: "Run a read-only shell command (grep, ls, find, git status, git log, etc.) in a read-only checkout; creates no branch, commit, or approval. The checkout is destroyed after this call.",
		Schema:      json.RawMessage(terminalSchema),
	}}
	terminal.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Repository string `json:"repository"`
			Command    string `json:"command"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return withEphemeralWorkspace(ctx, input.Repository, func(workspace string) (json.RawMessage, error) {
			return runTerminal(ctx, runner, workspace, input.Command)
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

	return []ports.Tool{readFile, terminal, metadata}
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
