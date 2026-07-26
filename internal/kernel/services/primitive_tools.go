package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// PrimitiveNames lists the kernel-owned primitive tools. Exactly one
// definition of each name may exist across the whole tool surface: an
// adapter or MCP server extends *around* the primitives with namespaced
// tools and never redefines one (see docs/adr/0006).
var PrimitiveNames = []string{"read_file", "write_file", "patch", "terminal"}

// WorkspaceResolver reports which checkout the current turn's primitives
// act on. It exists so the primitives depend on session state rather than
// on a repository argument or on one specific closured workspace.
type WorkspaceResolver interface {
	Resolve(ctx context.Context) (WorkspaceBinding, error)
}

// NewPrimitiveTools returns the single kernel-owned CRUD-over-a-workspace
// tool set: read_file, write_file, patch, and terminal. The same tool
// values are registered in the conversational registry and handed to the
// implementation loop, so a primitive name resolves to one definition and
// one implementation no matter which loop is running.
//
// The write primitives are always present. When the resolved workspace is
// read-only they fail with ErrWorkspaceReadOnly rather than vanishing from
// the model's tool list, so refusal is an observable result the model can
// reason about instead of a silent capability change.
func NewPrimitiveTools(workspaces WorkspaceResolver, runner ports.Runner, reader ports.RepositoryReader) []ports.Tool {
	resolve := func(ctx context.Context) (WorkspaceBinding, error) {
		if workspaces == nil {
			return WorkspaceBinding{}, ErrNoWorkspace
		}
		return workspaces.Resolve(ctx)
	}
	resolveWritable := func(ctx context.Context) (WorkspaceBinding, error) {
		binding, err := resolve(ctx)
		if err != nil {
			return WorkspaceBinding{}, err
		}
		if !binding.Writable {
			return WorkspaceBinding{}, ErrWorkspaceReadOnly
		}
		return binding, nil
	}

	readFile := repositoryTool{definition: ports.ToolDefinition{
		Name:        "read_file",
		Description: "Read a bounded range of lines from a file in this session's workspace.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`),
	}}
	readFile.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := decodeStrict(raw, &input); err != nil {
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

	terminal := repositoryTool{definition: ports.ToolDefinition{
		Name:        "terminal",
		Description: terminalDescription,
		Schema:      json.RawMessage(terminalSchema),
	}}
	terminal.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Command string `json:"command"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		binding, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		return runTerminal(ctx, runner, binding.Path, input.Command)
	}

	patch := repositoryTool{definition: ports.ToolDefinition{
		Name:        "patch",
		Description: "Replace one exact occurrence of old_string with new_string in an existing file in this session's workspace. Fails if old_string is not found or is not unique, or if the workspace is read-only.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"old_string":{"type":"string","minLength":1},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`),
	}}
	patch.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		binding, err := resolveWritable(ctx)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveWorkspacePath(binding.Path, input.Path)
		if err != nil {
			return nil, err
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", input.Path, err)
		}
		text := string(content)
		count := strings.Count(text, input.OldString)
		if count == 0 {
			return nil, fmt.Errorf("old_string not found in %s", input.Path)
		}
		if count > 1 {
			return nil, fmt.Errorf("old_string matches %d times in %s, must match exactly once", count, input.Path)
		}
		updated := strings.Replace(text, input.OldString, input.NewString, 1)
		if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", input.Path, err)
		}
		return json.Marshal(map[string]string{"status": "patched", "path": input.Path})
	}

	writeFile := repositoryTool{definition: ports.ToolDefinition{
		Name:        "write_file",
		Description: "Create a file or replace its full contents in this session's workspace. Creates parent directories as needed. Fails if the workspace is read-only.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}
	writeFile.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		binding, err := resolveWritable(ctx)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveWorkspacePath(binding.Path, input.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o700); err != nil {
			return nil, fmt.Errorf("create directories for %s: %w", input.Path, err)
		}
		if err := os.WriteFile(resolved, []byte(input.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", input.Path, err)
		}
		return json.Marshal(map[string]string{"status": "written", "path": input.Path})
	}

	return []ports.Tool{readFile, writeFile, patch, terminal}
}
