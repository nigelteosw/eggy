package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// PrimitiveNames lists the kernel-owned primitive tools. Exactly one
// definition of each name may exist across the whole tool surface: an
// adapter or MCP server extends *around* the primitives with namespaced
// tools and never redefines one (see docs/adr/0006).
var PrimitiveNames = []string{"read_file"}

// WorkspaceResolver reports which checkout the current turn's primitives
// act on. It exists so the primitives depend on session state rather than
// on a repository argument or on one specific closured workspace.
type WorkspaceResolver interface {
	Resolve(ctx context.Context) (WorkspaceBinding, error)
}

// NewPrimitiveTools returns Eggy's single kernel-owned, read-only workspace
// primitive. Shell execution and file mutation are intentionally absent.
func NewPrimitiveTools(workspaces WorkspaceResolver, reader ports.RepositoryReader) []ports.Tool {
	resolve := func(ctx context.Context) (WorkspaceBinding, error) {
		if workspaces == nil {
			return WorkspaceBinding{}, ErrNoWorkspace
		}
		return workspaces.Resolve(ctx)
	}

	readFile := repositoryTool{definition: ports.ToolDefinition{
		Name:        "read_file",
		Description: "Read a bounded range of lines from a file in this session's workspace.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`),
		Effect:      ports.ReadOnlyTool(),
	}}
	readFile.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := services.DecodeToolInput(raw, &input); err != nil {
			return nil, err
		}
		binding, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		if reader == nil {
			return nil, errors.New("read_file is unavailable")
		}
		content, err := reader.ReadFile(ctx, binding.Path, input.Path, input.StartLine, input.EndLine)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"path": input.Path, "content": content})
	}

	return []ports.Tool{readFile}
}
