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

var ErrPathOutsideWorkspace = errors.New("path escapes the workspace")

func resolveWorkspacePath(workspace, path string) (string, error) {
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	absoluteJoined, err := filepath.Abs(filepath.Join(workspace, path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absoluteWorkspace, absoluteJoined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideWorkspace
	}
	return absoluteJoined, nil
}

func newPatchTool() ports.Tool {
	return repositoryTool{
		definition: ports.ToolDefinition{
			Name:        "patch",
			Description: "Replace one exact occurrence of old_string with new_string in an existing file. Fails if old_string is not found or is not unique.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"old_string":{"type":"string","minLength":1},"new_string":{"type":"string"}},"required":["path","old_string","new_string"],"additionalProperties":false}`),
		},
		execute: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			workspace, ok := workspaceFromContext(ctx)
			if !ok {
				return nil, errors.New("patch is unavailable outside an implementation run")
			}
			var input struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}
			if err := decodeStrict(raw, &input); err != nil {
				return nil, err
			}
			resolved, err := resolveWorkspacePath(workspace, input.Path)
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
		},
	}
}

func newWriteFileTool() ports.Tool {
	return repositoryTool{
		definition: ports.ToolDefinition{
			Name:        "write_file",
			Description: "Create a file or replace its full contents. Creates parent directories as needed.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		},
		execute: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			workspace, ok := workspaceFromContext(ctx)
			if !ok {
				return nil, errors.New("write_file is unavailable outside an implementation run")
			}
			var input struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeStrict(raw, &input); err != nil {
				return nil, err
			}
			resolved, err := resolveWorkspacePath(workspace, input.Path)
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
		},
	}
}
