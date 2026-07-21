package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// NewImplementationTools returns the tools available inside a bounded
// repository_modify run: read_file and terminal resolve their workspace
// from ctx (set once per run via withWorkspace); patch, write_file, and
// finish_implementation are never registered outside this tool set.
func NewImplementationTools(runner ports.Runner, reader ports.RepositoryReader) []ports.Tool {
	readFile := repositoryTool{definition: ports.ToolDefinition{
		Name:        "read_file",
		Description: "Read a bounded range of lines from a file in the current checkout.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"start_line":{"type":"integer","minimum":1},"end_line":{"type":"integer","minimum":1}},"required":["path"],"additionalProperties":false}`),
	}}
	readFile.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		workspace, ok := workspaceFromContext(ctx)
		if !ok {
			return nil, errors.New("read_file is unavailable outside an implementation run")
		}
		var input struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		if reader == nil {
			return nil, errors.New("read_file is unavailable")
		}
		content, err := reader.ReadFile(ctx, workspace, input.Path, input.StartLine, input.EndLine)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"path": input.Path, "content": content})
	}

	terminal := repositoryTool{definition: ports.ToolDefinition{Name: "terminal", Description: terminalDescription, Schema: json.RawMessage(terminalSchema)}}
	terminal.execute = func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		workspace, ok := workspaceFromContext(ctx)
		if !ok {
			return nil, errors.New("terminal is unavailable outside an implementation run")
		}
		var input struct {
			Command string `json:"command"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		return runTerminal(ctx, runner, workspace, input.Command)
	}

	finish := repositoryTool{definition: ports.ToolDefinition{
		Name:        "finish_implementation",
		Description: "Call exactly once when the requested change is complete and validated. Ends the run.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","minLength":1},"validation":{"type":"string","minLength":1},"commit_message":{"type":"string","minLength":1},"changed_files":{"type":"array","items":{"type":"string"}}},"required":["summary","validation","commit_message"],"additionalProperties":false}`),
	}}
	finish.execute = func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Summary       string   `json:"summary"`
			Validation    string   `json:"validation"`
			CommitMessage string   `json:"commit_message"`
			ChangedFiles  []string `json:"changed_files"`
		}
		if err := decodeStrict(raw, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Summary) == "" {
			return nil, errors.New("summary must not be empty")
		}
		if strings.TrimSpace(input.Validation) == "" {
			return nil, errors.New("validation must not be empty: describe the build/test/lint command you ran and its result")
		}
		if strings.TrimSpace(input.CommitMessage) == "" {
			return nil, errors.New("commit_message must not be empty")
		}
		return json.Marshal(map[string]string{"status": "received"})
	}

	return []ports.Tool{readFile, terminal, newPatchTool(), newWriteFileTool(), finish}
}
