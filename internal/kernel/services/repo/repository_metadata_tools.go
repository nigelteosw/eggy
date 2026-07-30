package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// NewRepositoryMetadataTools registers the ordinary, non-primitive
// repository tools that read GitHub's control plane rather than a
// checkout. Repository contents are reached through read_file against the
// session's workspace, so there is no second, clone-per-call read path here.
func NewRepositoryMetadataTools(store ports.StateStore, reader ports.RepositoryReader) []ports.Tool {
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
		if err := services.DecodeToolInput(raw, &input); err != nil {
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
			summary, err := reader.ReviewSummary(ctx, repository, input.Number)
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

	return []ports.Tool{metadata}
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
