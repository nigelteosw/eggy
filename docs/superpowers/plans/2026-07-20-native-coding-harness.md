# Native Coding Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Codex CLI / Claude Code CLI subprocess coding-agent split with a native Go harness — `read_file`, `terminal`, `patch`, `write_file`, `finish_implementation` tools driven by the same selected model through `internal/kernel/agent.Loop` — so the model the owner already selected with `/model` does its own repository edits, with no separate CLI subprocess and no separate coding-agent selection.

**Architecture:** `agent.Loop` gains `RunImplementation`, a bounded tool-loop variant that returns a required terminal tool's arguments instead of a chat reply. `CodingService.Start` (clone → branch → diff → approval, unchanged) calls a new `Implementer` instead of the old `ports.CodingAgent`; `NativeImplementer` is the concrete implementation, wrapping a dedicated `*agent.Loop` scoped to five new Go-native tools. The outer conversational loop's `repository_tree`/`search`/`status` tools are consolidated into one `terminal` tool (the model runs `grep`/`ls`/`git status` itself); `repository_read` is renamed `read_file`. Codex CLI, Claude Code CLI, `/coding_agent`, and all their config/env plumbing are deleted.

**Tech Stack:** Go 1.26, standard library only (`os`, `path/filepath`, `os/exec` via the existing `ports.Runner`). No new dependencies.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral — no provider-specific imports. (`AGENTS.md`)
- Register adapters and tools only through `internal/bootstrap`. (`AGENTS.md`)
- Treat configured repositories as trusted, but keep path, environment, timeout, output, and process-group restrictions intact. (`AGENTS.md`)
- Never weaken independent approval checks for commit, push, or pull-request creation. Protected branches remain unpushable even with approval. This plan does not touch that pipeline at all — only what runs *before* the diff is captured changes. (`AGENTS.md`, spec's "Safety model (unchanged)")
- Add or change behavior test-first; run the focused test before the full suite. (`AGENTS.md`)
- Do not introduce a web framework, ORM, DI framework, agent framework, native plugin runtime, or database. (`AGENTS.md`)
- Preserve `/data/state.json` schema compatibility — this plan adds no new persisted fields. (`AGENTS.md`)
- `ports.RepositoryReader`'s `ListTree`/`Search`/`Status`/`Branches` methods and their GitHub-adapter implementation are left in place, unused by any tool after this plan. Removing them means touching `internal/adapters/repositories/github` and its test suite for no functional gain — out of scope here; note it as a follow-up if it bothers you later.
- Full spec: `docs/superpowers/specs/2026-07-20-native-coding-harness-design.md`.

---

## Task 1: `Loop.RunImplementation`

**Files:**
- Modify: `internal/kernel/agent/loop.go`
- Test: `internal/kernel/agent/loop_test.go`

**Interfaces:**
- Consumes: `l.selected map[string]ModelTarget`, `l.tools map[string]ports.Tool`, `l.defs []ports.ToolDefinition`, `l.selectedMaxSteps int` (all already fields on `Loop`), `ErrUnknownTool`, `ErrToolStepLimit` (already defined in this file).
- Produces: `func (l *Loop) RunImplementation(ctx context.Context, alias string, messages []ports.Message, terminalTool string, onToolCall func(name string)) (json.RawMessage, ports.ModelUsage, error)` and `var ErrTerminalToolNotCalled error` — both consumed by Task 3's `NativeImplementer`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/kernel/agent/loop_test.go` (same package, so `queuedModel` and `fakeTool` from the existing file are already in scope):

```go
func TestRunImplementationReturnsTerminalToolArguments(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "finish_implementation", Arguments: json.RawMessage(`{"summary":"done","commit_message":"feat: done"}`)}}}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"hi"}`)},
		&fakeTool{name: "finish_implementation", result: json.RawMessage(`{"status":"received"}`)},
	}, nil, 4)

	raw, _, err := loop.RunImplementation(context.Background(), "model", []ports.Message{{Role: ports.RoleUser, Content: "implement"}}, "finish_implementation", nil)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Summary       string `json:"summary"`
		CommitMessage string `json:"commit_message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Summary != "done" || result.CommitMessage != "feat: done" {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}

func TestRunImplementationRetriesAfterTerminalToolValidationError(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "finish_implementation", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "finish_implementation", Arguments: json.RawMessage(`{"summary":"done","commit_message":"feat: done"}`)}}}},
	}}
	finish := &sequencedTool{name: "finish_implementation", errs: []error{errors.New("summary must not be empty"), nil}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{finish}, nil, 4)

	raw, _, err := loop.RunImplementation(context.Background(), "model", []ports.Message{{Role: ports.RoleUser, Content: "implement"}}, "finish_implementation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if finish.calls != 2 {
		t.Fatalf("calls=%d, want 2", finish.calls)
	}
	var result struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Summary != "done" {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}

func TestRunImplementationFailsWhenStepLimitReachedWithoutTerminalTool(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{}`)},
	}, nil, 1)

	_, _, err := loop.RunImplementation(context.Background(), "model", []ports.Message{{Role: ports.RoleUser, Content: "implement"}}, "finish_implementation", nil)
	if !errors.Is(err, ErrToolStepLimit) {
		t.Fatalf("err=%v, want ErrToolStepLimit", err)
	}
}

func TestRunImplementationReportsUnknownModelAlias(t *testing.T) {
	loop := NewSelectedLoop(nil, nil, nil, 4)
	if _, _, err := loop.RunImplementation(context.Background(), "missing", nil, "finish_implementation", nil); err == nil {
		t.Fatal("expected unknown alias error")
	}
}

func TestRunImplementationInvokesOnToolCallForNonTerminalTools(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "terminal", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "finish_implementation", Arguments: json.RawMessage(`{"summary":"done","commit_message":"feat: done"}`)}}}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "terminal", result: json.RawMessage(`{}`)},
		&fakeTool{name: "finish_implementation", result: json.RawMessage(`{}`)},
	}, nil, 4)
	var called []string
	if _, _, err := loop.RunImplementation(context.Background(), "model", []ports.Message{{Role: ports.RoleUser, Content: "implement"}}, "finish_implementation", func(name string) { called = append(called, name) }); err != nil {
		t.Fatal(err)
	}
	if len(called) != 1 || called[0] != "terminal" {
		t.Fatalf("called=%v", called)
	}
}

type sequencedTool struct {
	name    string
	results []json.RawMessage
	errs    []error
	calls   int
}

func (t *sequencedTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (t *sequencedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	i := t.calls
	t.calls++
	var result json.RawMessage
	var err error
	if i < len(t.results) {
		result = t.results[i]
	}
	if i < len(t.errs) {
		err = t.errs[i]
	}
	return result, err
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kernel/agent/... -run TestRunImplementation -v`
Expected: FAIL with `loop.RunImplementation undefined`.

- [ ] **Step 3: Implement `RunImplementation`**

Add to `internal/kernel/agent/loop.go`, after the existing `RunSelected` method:

```go
// ErrTerminalToolNotCalled is returned when RunImplementation exhausts a
// model turn without any tool call, before the terminal tool was ever
// successfully called.
var ErrTerminalToolNotCalled = errors.New("implementation run ended without a terminal tool call")

// RunImplementation drives the loop until the model successfully calls
// terminalTool, returning that call's raw arguments, or the step limit is
// reached first. Every tool registered on l is available unconditionally —
// callers construct a Loop instance scoped to exactly the tools an
// implementation run should have, rather than relying on lane filtering.
// onToolCall, if non-nil, fires after each successful non-terminal tool call
// for progress reporting; it does not fire for the terminal tool itself.
func (l *Loop) RunImplementation(ctx context.Context, alias string, messages []ports.Message, terminalTool string, onToolCall func(name string)) (json.RawMessage, ports.ModelUsage, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return nil, ports.ModelUsage{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	messages = append([]ports.Message(nil), messages...)
	usage := ports.ModelUsage{}
	steps := 0
	for {
		response, err := target.Model.Generate(ctx, ports.ModelRequest{Model: target.ModelID, Messages: messages, Tools: l.defs})
		if err != nil {
			return nil, usage, err
		}
		usage = usage.Add(response.Usage)
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			return nil, usage, ErrTerminalToolNotCalled
		}
		if steps >= l.selectedMaxSteps {
			return nil, usage, ErrToolStepLimit
		}
		messages = append(messages, assistant)
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return nil, usage, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			output, toolErr := tool.Execute(ctx, call.Arguments)
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
				messages = append(messages, ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)})
				continue
			}
			if call.Name == terminalTool {
				return call.Arguments, usage, nil
			}
			if onToolCall != nil {
				onToolCall(call.Name)
			}
			messages = append(messages, ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)})
		}
		steps++
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/kernel/agent/... -run TestRunImplementation -v`
Expected: PASS (all five new tests).

Run the full package too: `go test ./internal/kernel/agent/...`
Expected: PASS (existing `TestLoop*` tests still pass — `RunImplementation` is additive).

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/agent/loop.go internal/kernel/agent/loop_test.go
git commit -m "feat: add Loop.RunImplementation for terminal-tool-driven runs"
```

---

## Task 2: Implementation tool primitives

**Files:**
- Create: `internal/kernel/services/workspace_context.go`
- Create: `internal/kernel/services/terminal_tool.go`
- Create: `internal/kernel/services/file_edit_tools.go`
- Create: `internal/kernel/services/implementation_tools.go`
- Test: `internal/kernel/services/implementation_tools_test.go`

**Interfaces:**
- Consumes: `repositoryTool` struct (unexported, defined in `internal/kernel/services/repository_tools.go`, has `definition ports.ToolDefinition` and `execute func(context.Context, json.RawMessage) (json.RawMessage, error)` fields, already used package-wide), `decodeStrict` (`internal/kernel/services/tools.go`), `ports.Tool`, `ports.RepositoryReader.ReadFile`, `ports.Runner.Execute`, `ports.Command`.
- Produces: `withWorkspace(ctx, workspace string) context.Context`, `workspaceFromContext(ctx) (string, bool)`, `runTerminal(ctx, runner ports.Runner, workspace, command string) (json.RawMessage, error)`, `terminalDescription string`, `terminalSchema string`, `func NewImplementationTools(runner ports.Runner, reader ports.RepositoryReader) []ports.Tool` — all consumed by Task 3 (`NativeImplementer`), Task 4 (`repository_read_tools.go`'s outer `terminal` tool reuses `runTerminal`/`terminalSchema`), and Task 5 (bootstrap wiring).

- [ ] **Step 1: Write the failing tests**

Create `internal/kernel/services/implementation_tools_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestReadFileToolRequiresWorkspaceContext(t *testing.T) {
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{content: "line one\n"})
	byName := implementationToolsByName(tools)
	if _, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"path":"main.go"}`)); err == nil {
		t.Fatal("expected error outside an implementation run")
	}
}

func TestReadFileToolReadsFromContextWorkspace(t *testing.T) {
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{content: "line one\n"})
	byName := implementationToolsByName(tools)
	ctx := withWorkspace(context.Background(), "/tmp/run-1")
	result, err := byName["read_file"].Execute(ctx, json.RawMessage(`{"path":"main.go"}`))
	if err != nil || !strings.Contains(string(result), "line one") {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func TestTerminalToolRunsInContextWorkspace(t *testing.T) {
	runner := &recordingRunner{result: ports.CommandResult{Stdout: "README.md\n"}}
	tools := NewImplementationTools(runner, &fakeRepositoryReader{})
	byName := implementationToolsByName(tools)
	ctx := withWorkspace(context.Background(), "/tmp/run-1")
	result, err := byName["terminal"].Execute(ctx, json.RawMessage(`{"command":"ls"}`))
	if err != nil || !strings.Contains(string(result), "README.md") {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if runner.command.Dir != "/tmp/run-1" || runner.command.Argv[0] != "sh" || runner.command.Argv[2] != "ls" {
		t.Fatalf("command=%#v", runner.command)
	}
}

func TestPatchToolReplacesUniqueMatchAndRejectsAmbiguousOrMissingMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc old() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{})
	byName := implementationToolsByName(tools)
	ctx := withWorkspace(context.Background(), dir)

	if _, err := byName["patch"].Execute(ctx, json.RawMessage(`{"path":"main.go","old_string":"func old()","new_string":"func new()"}`)); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "func new()") {
		t.Fatalf("updated=%s", updated)
	}

	if _, err := byName["patch"].Execute(ctx, json.RawMessage(`{"path":"main.go","old_string":"func missing()","new_string":"x"}`)); err == nil {
		t.Fatal("expected not-found error")
	}
	if err := os.WriteFile(path, []byte("func a() {}\nfunc a() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["patch"].Execute(ctx, json.RawMessage(`{"path":"main.go","old_string":"func a() {}","new_string":"x"}`)); err == nil {
		t.Fatal("expected ambiguous-match error")
	}
}

func TestPatchToolRejectsPathEscapingWorkspace(t *testing.T) {
	dir := t.TempDir()
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{})
	byName := implementationToolsByName(tools)
	ctx := withWorkspace(context.Background(), dir)
	if _, err := byName["patch"].Execute(ctx, json.RawMessage(`{"path":"../../etc/passwd","old_string":"a","new_string":"b"}`)); err == nil {
		t.Fatal("expected path-escape error")
	}
}

func TestWriteFileToolCreatesFileAndParentDirectories(t *testing.T) {
	dir := t.TempDir()
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{})
	byName := implementationToolsByName(tools)
	ctx := withWorkspace(context.Background(), dir)
	if _, err := byName["write_file"].Execute(ctx, json.RawMessage(`{"path":"pkg/new.go","content":"package pkg\n"}`)); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "pkg/new.go"))
	if err != nil || string(content) != "package pkg\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestFinishImplementationToolRequiresSummaryAndCommitMessage(t *testing.T) {
	tools := NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{})
	byName := implementationToolsByName(tools)
	if _, err := byName["finish_implementation"].Execute(context.Background(), json.RawMessage(`{"commit_message":"x"}`)); err == nil {
		t.Fatal("expected summary required error")
	}
	if _, err := byName["finish_implementation"].Execute(context.Background(), json.RawMessage(`{"summary":"done"}`)); err == nil {
		t.Fatal("expected commit_message required error")
	}
	result, err := byName["finish_implementation"].Execute(context.Background(), json.RawMessage(`{"summary":"done","commit_message":"feat: done"}`))
	if err != nil || !strings.Contains(string(result), "received") {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func implementationToolsByName(tools []ports.Tool) map[string]ports.Tool {
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	return byName
}

type recordingRunner struct {
	command ports.Command
	result  ports.CommandResult
}

func (r *recordingRunner) Create(context.Context, string) (string, error) { return "", nil }
func (r *recordingRunner) Execute(_ context.Context, command ports.Command) (ports.CommandResult, error) {
	r.command = command
	return r.result, nil
}
func (r *recordingRunner) Destroy(context.Context, string) error { return nil }
```

`fakeWorkspaceRunner` (from `coding_test.go`) and `fakeRepositoryReader` (from `repository_read_tools_test.go`) are in the same package and already in scope.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kernel/services/... -run "TestReadFileTool|TestTerminalTool|TestPatchTool|TestWriteFileTool|TestFinishImplementationTool" -v`
Expected: FAIL with `NewImplementationTools undefined` (and related undefined symbols).

- [ ] **Step 3: Create `workspace_context.go`**

```go
package services

import "context"

type workspaceContextKey struct{}

// withWorkspace attaches the active implementation run's workspace
// directory to ctx so tools registered once at bootstrap can resolve it
// per call, instead of being closured over one specific workspace.
func withWorkspace(ctx context.Context, workspace string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, workspace)
}

func workspaceFromContext(ctx context.Context) (string, bool) {
	workspace, ok := ctx.Value(workspaceContextKey{}).(string)
	return workspace, ok && workspace != ""
}
```

- [ ] **Step 4: Create `terminal_tool.go`**

```go
package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

const terminalDescription = "Run a shell command (e.g. grep, ls, find, git status, git log, go test) in the repository checkout. Output is captured and bounded; the command runs with restricted environment and a timeout."
const terminalSchema = `{"type":"object","properties":{"command":{"type":"string","minLength":1}},"required":["command"],"additionalProperties":false}`

func runTerminal(ctx context.Context, runner ports.Runner, workspace, command string) (json.RawMessage, error) {
	if runner == nil {
		return nil, errors.New("terminal is unavailable")
	}
	result, err := runner.Execute(ctx, ports.Command{Argv: []string{"sh", "-c", command}, Dir: workspace})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode, "output_truncated": result.OutputTruncated,
	})
}
```

- [ ] **Step 5: Create `file_edit_tools.go`**

```go
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
```

- [ ] **Step 6: Create `implementation_tools.go`**

```go
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
		Schema:      json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","minLength":1},"validation":{"type":"string"},"commit_message":{"type":"string","minLength":1},"changed_files":{"type":"array","items":{"type":"string"}}},"required":["summary","commit_message"],"additionalProperties":false}`),
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
		if strings.TrimSpace(input.CommitMessage) == "" {
			return nil, errors.New("commit_message must not be empty")
		}
		return json.Marshal(map[string]string{"status": "received"})
	}

	return []ports.Tool{readFile, terminal, newPatchTool(), newWriteFileTool(), finish}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/kernel/services/... -run "TestReadFileTool|TestTerminalTool|TestPatchTool|TestWriteFileTool|TestFinishImplementationTool" -v`
Expected: PASS (all seven tests).

Run the full package: `go test ./internal/kernel/services/...`
Expected: PASS (no existing test touched this package's exported surface yet).

- [ ] **Step 8: Commit**

```bash
git add internal/kernel/services/workspace_context.go internal/kernel/services/terminal_tool.go internal/kernel/services/file_edit_tools.go internal/kernel/services/implementation_tools.go internal/kernel/services/implementation_tools_test.go
git commit -m "feat: add read_file/terminal/patch/write_file/finish_implementation tools"
```

---

## Task 3: `NativeImplementer`

**Files:**
- Create: `internal/kernel/services/implementer.go`
- Test: `internal/kernel/services/implementer_test.go`

**Interfaces:**
- Consumes: `agent.Loop.RunImplementation` (Task 1), `NewImplementationTools` (Task 2), `agent.NewSelectedLoop`, `agent.ModelTarget`.
- Produces: `type Implementer interface { Implement(ctx, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error); Interrupt(runID string) error }` and `func NewNativeImplementer(loop *agent.Loop, aliasFor func(context.Context) (string, error)) *NativeImplementer` — both consumed by Task 5 (`CodingService` cutover and bootstrap wiring).

- [ ] **Step 1: Write the failing tests**

Create `internal/kernel/services/implementer_test.go`:

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestNativeImplementerReturnsStructuredResultAndReportsToolProgress(t *testing.T) {
	model := &sequencedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "terminal", Arguments: json.RawMessage(`{"command":"ls"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "finish_implementation", Arguments: json.RawMessage(`{"summary":"done","commit_message":"feat: done","changed_files":["main.go"]}`)}}}},
	}}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	var updates []ports.CodingProgress
	result, err := implementer.Implement(context.Background(), "run-1", "/tmp/run-1", "fix the bug", func(p ports.CodingProgress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "done" || result.CommitMessage != "feat: done" || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "main.go" {
		t.Fatalf("result=%#v", result)
	}
	if len(updates) != 1 || updates[0].Kind != "tool" || updates[0].RunID != "run-1" || updates[0].Message != "used terminal" {
		t.Fatalf("updates=%#v", updates)
	}
}

func TestNativeImplementerRejectsConcurrentRunsWithSameID(t *testing.T) {
	block := &blockingModel{unblock: make(chan struct{}), started: make(chan struct{})}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: block, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	go func() { _, _ = implementer.Implement(context.Background(), "run-1", "/tmp/run-1", "fix the bug", nil) }()
	<-block.started
	if _, err := implementer.Implement(context.Background(), "run-1", "/tmp/run-1", "fix the bug", nil); err == nil {
		t.Fatal("expected already-active error")
	}
	close(block.unblock)
}

func TestNativeImplementerInterruptCancelsActiveRun(t *testing.T) {
	block := &blockingModel{unblock: make(chan struct{}), started: make(chan struct{})}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: block, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	done := make(chan error, 1)
	go func() { _, err := implementer.Implement(context.Background(), "run-1", "/tmp/run-1", "fix the bug", nil); done <- err }()
	<-block.started
	if err := implementer.Interrupt("run-1"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if err := implementer.Interrupt("run-1"); err == nil {
		t.Fatal("expected not-found error after completion")
	}
}

func TestNativeImplementerFailsWhenAliasResolutionFails(t *testing.T) {
	loop := agent.NewSelectedLoop(nil, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "", errors.New("no model selected") })
	if _, err := implementer.Implement(context.Background(), "run-1", "/tmp/run-1", "fix the bug", nil); err == nil {
		t.Fatal("expected alias resolution error")
	}
}

type sequencedModel struct {
	responses []ports.ModelResponse
}

func (m *sequencedModel) Generate(_ context.Context, _ ports.ModelRequest) (ports.ModelResponse, error) {
	if len(m.responses) == 0 {
		return ports.ModelResponse{}, errors.New("no response queued")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

type blockingModel struct {
	unblock chan struct{}
	started chan struct{}
}

func (m *blockingModel) Generate(ctx context.Context, _ ports.ModelRequest) (ports.ModelResponse, error) {
	close(m.started)
	select {
	case <-ctx.Done():
		return ports.ModelResponse{}, ctx.Err()
	case <-m.unblock:
		return ports.ModelResponse{}, errors.New("unexpected unblock")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kernel/services/... -run TestNativeImplementer -v`
Expected: FAIL with `NewNativeImplementer undefined`.

- [ ] **Step 3: Implement `implementer.go`**

```go
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

const implementationSystemPrompt = `Eggy implementation contract
- You are editing a single, already-cloned, already-branched Git checkout. Work only inside it.
- Do not run git commit, git push, git branch, git checkout, or any command that creates, switches, renames, or deletes a branch, or changes HEAD. Eggy performs each of those only after its own independent owner approval.
- Use read_file and terminal to explore the checkout. Use patch to make an exact, minimal edit to an existing file (old_string must match exactly once). Use write_file to create a new file or fully replace one.
- Run this repository's own build/test/lint commands via terminal to validate your change before finishing, and report what you ran in the validation field.
- When the change is complete and validated, call finish_implementation exactly once with a non-empty summary of what changed and why, a validation field describing what you ran and its result, a commit_message suitable for the change, and changed_files listing every file path you modified or created.`

// Implementer runs the bounded, tool-driven implementation loop against an
// already-prepared workspace and returns its structured result.
type Implementer interface {
	Implement(ctx context.Context, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error)
	Interrupt(runID string) error
}

// NativeImplementer drives agent.Loop.RunImplementation with Eggy's own
// file/terminal tools instead of an external CLI subprocess.
type NativeImplementer struct {
	loop     *agent.Loop
	aliasFor func(context.Context) (string, error)

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func NewNativeImplementer(loop *agent.Loop, aliasFor func(context.Context) (string, error)) *NativeImplementer {
	return &NativeImplementer{loop: loop, aliasFor: aliasFor, active: map[string]context.CancelFunc{}}
}

func (n *NativeImplementer) Implement(ctx context.Context, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	runContext, cancel := context.WithCancel(ctx)
	n.mu.Lock()
	if _, exists := n.active[runID]; exists {
		n.mu.Unlock()
		cancel()
		return ports.CodingResult{}, fmt.Errorf("coding run %q is already active", runID)
	}
	n.active[runID] = cancel
	n.mu.Unlock()
	defer func() {
		cancel()
		n.mu.Lock()
		delete(n.active, runID)
		n.mu.Unlock()
	}()

	alias, err := n.aliasFor(runContext)
	if err != nil {
		return ports.CodingResult{}, err
	}
	runContext = withWorkspace(runContext, workspace)
	messages := []ports.Message{
		{Role: ports.RoleSystem, Content: implementationSystemPrompt},
		{Role: ports.RoleUser, Content: instruction},
	}
	onToolCall := func(name string) {
		if progress != nil {
			progress(ports.CodingProgress{Kind: "tool", Message: "used " + name, RunID: runID})
		}
	}
	raw, _, err := n.loop.RunImplementation(runContext, alias, messages, "finish_implementation", onToolCall)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.CodingResult{}, err
		}
		return ports.CodingResult{}, fmt.Errorf("implementation run failed: %w", err)
	}
	var structured struct {
		Summary       string   `json:"summary"`
		Validation    string   `json:"validation"`
		CommitMessage string   `json:"commit_message"`
		ChangedFiles  []string `json:"changed_files"`
	}
	if err := json.Unmarshal(raw, &structured); err != nil {
		return ports.CodingResult{}, errors.New("finish_implementation produced an invalid result")
	}
	return ports.CodingResult{Summary: structured.Summary, Validation: structured.Validation, CommitMessage: structured.CommitMessage, ChangedFiles: structured.ChangedFiles}, nil
}

func (n *NativeImplementer) Interrupt(runID string) error {
	n.mu.Lock()
	cancel, ok := n.active[runID]
	n.mu.Unlock()
	if !ok {
		return fmt.Errorf("coding run %q is not active", runID)
	}
	cancel()
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/kernel/services/... -run TestNativeImplementer -race -v`
Expected: PASS (all four tests; `-race` matters here since `NativeImplementer` uses a mutex and goroutines).

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/services/implementer.go internal/kernel/services/implementer_test.go
git commit -m "feat: add NativeImplementer wrapping Loop.RunImplementation"
```

---

## Task 4: Consolidate outer read-only tools onto `read_file`/`terminal`

**Files:**
- Modify: `internal/kernel/services/repository_read_tools.go`
- Modify: `internal/kernel/services/repository_read_tools_test.go`

**Interfaces:**
- Consumes: `runTerminal`, `terminalSchema` (Task 2), `lookupRepository` (already in `repository_tools.go`), `decodeStrict`.
- Produces: `NewRepositoryReadTools(...)` keeps its existing signature `(store ports.StateStore, runner ports.Runner, checkout ports.RepositoryCheckout, reader ports.RepositoryReader, newRunID func() string) []ports.Tool` — bootstrap's call site in `app.go` needs no change — but now returns tools named `read_file`, `terminal`, `repository_github` instead of `repository_tree`, `repository_search`, `repository_read`, `repository_status`, `repository_github`.

- [ ] **Step 1: Write the failing test**

Replace the contents of `internal/kernel/services/repository_read_tools_test.go` with:

```go
package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryReadToolsCloneIntoEphemeralWorkspaceAndDestroyIt(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	runner := &fakeReadWorkspaceRunner{workspace: "/tmp/runs/read-1", commandResult: ports.CommandResult{Stdout: "README.md\n"}}
	reader := &fakeRepositoryReader{
		content: "line one\n",
		summary: ports.RepositorySummary{Title: "eggy", DefaultBranch: "main"},
		checks:  []ports.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}},
	}
	tools := NewRepositoryReadTools(store, runner, reader, reader, func() string { return "run-1" })
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}

	read, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","path":"README.md"}`))
	if err != nil || !strings.Contains(string(read), "line one") {
		t.Fatalf("read=%s err=%v", read, err)
	}
	terminal, err := byName["terminal"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","command":"ls"}`))
	if err != nil || !strings.Contains(string(terminal), "README.md") {
		t.Fatalf("terminal=%s err=%v", terminal, err)
	}
	if runner.command.Dir != "/tmp/runs/read-1" || runner.command.Argv[0] != "sh" {
		t.Fatalf("command=%#v", runner.command)
	}
	summary, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"repository"}`))
	if err != nil || !strings.Contains(string(summary), "eggy") {
		t.Fatalf("summary=%s err=%v", summary, err)
	}
	checks, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"checks","ref":"abc123"}`))
	if err != nil || !strings.Contains(string(checks), "build") {
		t.Fatalf("checks=%s err=%v", checks, err)
	}
	if reader.cloned != 2 {
		t.Fatalf("expected 2 clones for read_file and terminal; repository_github must not clone, got %d", reader.cloned)
	}
	if !runner.created || !runner.destroyed {
		t.Fatalf("runner=%#v", runner)
	}
}

func TestRepositoryReadToolsRejectUnknownRepositoryAndUnsupportedKind(t *testing.T) {
	store := newMemoryStore()
	runner := &fakeReadWorkspaceRunner{workspace: "/tmp/runs/read-2"}
	reader := &fakeRepositoryReader{}
	tools := NewRepositoryReadTools(store, runner, reader, reader, func() string { return "run-2" })
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	if _, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"repository":"missing","path":"README.md"}`)); err == nil {
		t.Fatal("expected unknown repository error")
	}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	if _, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"issue"}`)); err == nil || !strings.Contains(err.Error(), "number is required") {
		t.Fatalf("error=%v", err)
	}
	if _, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"bogus"}`)); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("error=%v", err)
	}
}

type fakeReadWorkspaceRunner struct {
	workspace          string
	created, destroyed bool
	command            ports.Command
	commandResult      ports.CommandResult
}

func (r *fakeReadWorkspaceRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *fakeReadWorkspaceRunner) Execute(_ context.Context, command ports.Command) (ports.CommandResult, error) {
	r.command = command
	return r.commandResult, nil
}
func (r *fakeReadWorkspaceRunner) Destroy(context.Context, string) error { r.destroyed = true; return nil }
```

`fakeRepositoryReader` stays exactly as already defined earlier in this same file (its `entries`/`matches`/`status`/`branches` fields become unused by these two tests but the type is untouched — `ports.RepositoryReader` keeps its full method set per this plan's Global Constraints, so nothing about the fake needs to change).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kernel/services/... -run TestRepositoryReadTools -v`
Expected: FAIL — `byName["read_file"]` is nil (tool doesn't exist yet under that name).

- [ ] **Step 3: Rewrite `repository_read_tools.go`**

Replace its entire contents with:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kernel/services/... -run TestRepositoryReadTools -v`
Expected: PASS.

Run the full package: `go test ./internal/kernel/services/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/kernel/services/repository_read_tools.go internal/kernel/services/repository_read_tools_test.go
git commit -m "refactor: consolidate repository_tree/search/status into terminal"
```

---

## Task 5: Cut over — delete the CLI coding agents, wire in the native implementer

This is one task because Go's whole-program compilation makes these changes mutually dependent: `CodingService` can't take an `Implementer` while `app.go` still constructs `ports.CodingAgent`-typed adapters, and `commands.go`/`prompt.go` reference fields this same change removes from `App`. Everything below lands in one commit.

**Files:**
- Modify: `internal/ports/ports.go`
- Modify: `internal/kernel/services/coding.go`
- Modify: `internal/kernel/services/coding_test.go`
- Modify: `internal/kernel/agent/prompt.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/bootstrap/commands_test.go`
- Modify: `internal/kernel/agent/prompt_test.go`
- Delete: `internal/adapters/coding/claudecli/` (all files)
- Delete: `internal/adapters/coding/codexcli/` (all files)
- Delete: `internal/kernel/services/coding_runtime.go`
- Delete: `internal/kernel/services/coding_runtime_test.go`

**Interfaces:**
- Consumes: `Implementer`, `NewNativeImplementer` (Task 3), `NewImplementationTools` (Task 2), `agent.NewSelectedLoop`, `AgentRuntime.SelectedModel(ctx) (string, error)` (already exists, unchanged).
- Produces: `CodingService.NewCodingService(store, runner, repository, implementer Implementer, now)`, `App.implementationLoop *agent.Loop` — no further tasks in this plan consume these; they're the terminal wiring.

- [ ] **Step 1: Write the failing test — update `coding_test.go`**

`CodingService`'s dependency type is changing from `ports.CodingAgent` to the new `Implementer` interface (Task 3), so its existing tests must change to match before the production code compiles against the new shape. Replace `internal/kernel/services/coding_test.go`'s contents with:

```go
package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodingServiceRunsImplementerCapturesDiffAndPersistsResult(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}
	repository := &fakeRepository{}
	implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", Validation: "tests pass", CommitMessage: "feat: done"}}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewCodingService(store, runner, repository, implementer, func() time.Time { return now })
	var updates []ports.CodingProgress
	run, result, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", func(progress ports.CodingProgress) { updates = append(updates, progress) })
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || run.Diff != "diff" || run.Branch != "eggy/run-1" || run.BaseRevision != "abc123" || result.CommitMessage != "feat: done" {
		t.Fatalf("run=%#v result=%#v", run, result)
	}
	if !runner.created || repository.clones != 1 || repository.branches != 1 || implementer.runID != "run-1" || implementer.workspace != runner.workspace || !strings.Contains(implementer.instruction, "Do not create, switch, rename, or delete branches") || !strings.Contains(implementer.instruction, "Do not commit, push, or create pull requests") {
		t.Fatalf("runner=%#v repository=%#v implementer=%#v", runner, repository, implementer)
	}
	state, _ := store.Load(context.Background())
	if state.CodingRuns["run-1"].Status != "completed" {
		t.Fatalf("state=%#v", state.CodingRuns)
	}
	var checkpoints []string
	for _, update := range updates {
		if update.Kind == "checkpoint" {
			checkpoints = append(checkpoints, update.Message)
		}
	}
	wantCheckpoints := []string{
		"Preparing an isolated workspace for eggy",
		"Cloning eggy@main",
		"Creating branch eggy/run-1",
		"Starting the implementation run",
		"Capturing diff and validation evidence",
	}
	if strings.Join(checkpoints, "|") != strings.Join(wantCheckpoints, "|") {
		t.Fatalf("checkpoints = %#v, want %#v", checkpoints, wantCheckpoints)
	}
}

func TestCodingServiceRejectsBranchOrHeadChangesBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeRepository)
		want   string
	}{
		{name: "branch", mutate: func(repository *fakeRepository) { repository.branch = "feat/unapproved" }, want: "branch"},
		{name: "head", mutate: func(repository *fakeRepository) { repository.head = "unapproved-commit" }, want: "HEAD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			store.state.CodingRuns = map[string]ports.CodingRun{}
			repository := &fakeRepository{}
			implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", CommitMessage: "feat: done"}, onRun: func() { test.mutate(repository) }}
			service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}, repository, implementer, time.Now)

			_, _, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
			state, _ := store.Load(context.Background())
			if state.CodingRuns["run-1"].Status != "failed" {
				t.Fatalf("run=%#v", state.CodingRuns["run-1"])
			}
		})
	}
}

func TestCodingServiceRecoversInterruptedRunsAndCleansWorkspace(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Workspace: "/tmp/runs/run", Status: "running"}}
	runner := &fakeWorkspaceRunner{}
	service := NewCodingService(store, runner, &fakeRepository{}, &fakeImplementer{}, time.Now)
	count, err := service.RecoverInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	state, _ := store.Load(context.Background())
	if state.CodingRuns["run"].Status != "interrupted" {
		t.Fatalf("run=%#v", state.CodingRuns["run"])
	}
	if err := service.Cleanup(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if !runner.destroyed {
		t.Fatal("workspace not destroyed")
	}
	state, _ = store.Load(context.Background())
	if state.CodingRuns["run"].Workspace != "" {
		t.Fatalf("workspace retained in state: %#v", state.CodingRuns["run"])
	}
}

type fakeWorkspaceRunner struct {
	workspace          string
	created, destroyed bool
}

func (r *fakeWorkspaceRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *fakeWorkspaceRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *fakeWorkspaceRunner) Destroy(context.Context, string) error { r.destroyed = true; return nil }

type fakeImplementer struct {
	runID, workspace, instruction string
	result                        ports.CodingResult
	onRun                         func()
}

func (a *fakeImplementer) Implement(_ context.Context, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	a.runID, a.workspace, a.instruction = runID, workspace, instruction
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "working"})
	}
	if a.onRun != nil {
		a.onRun()
	}
	return a.result, nil
}
func (a *fakeImplementer) Interrupt(string) error { return nil }
```

(`fakeRepository` stays defined in `shipping_test.go`, untouched.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kernel/services/... -run TestCodingService -v`
Expected: FAIL — `NewCodingService`'s fourth parameter doesn't accept `*fakeImplementer` yet (still typed `ports.CodingAgent`).

- [ ] **Step 3: Update `internal/kernel/services/coding.go`**

Change the struct, constructor, and the two call sites that reference the old field:

```go
type CodingService struct {
	store       ports.StateStore
	runner      ports.Runner
	repository  ports.CodingRepository
	implementer Implementer
	now         func() time.Time
}

func NewCodingService(store ports.StateStore, runner ports.Runner, repository ports.CodingRepository, implementer Implementer, now func() time.Time) *CodingService {
	return &CodingService{store: store, runner: runner, repository: repository, implementer: implementer, now: now}
}
```

Inside `Start`, change:

```go
	checkpoint(progress, "Starting the coding agent")
	result, err := s.agent.Run(ctx, ports.CodingRequest{RunID: runID, Workspace: workspace, Instruction: prompt}, progress)
```

to:

```go
	checkpoint(progress, "Starting the implementation run")
	result, err := s.implementer.Implement(ctx, runID, workspace, prompt, progress)
```

Change `Stop`:

```go
func (s *CodingService) Stop(runID string) error { return s.agent.Interrupt(runID) }
```

to:

```go
func (s *CodingService) Stop(runID string) error { return s.implementer.Interrupt(runID) }
```

- [ ] **Step 4: Update `internal/ports/ports.go`**

Remove the `CodingRequest` struct and the `CodingAgent` interface (keep `CodingProgress` and `CodingResult`, both still used):

```go
type CodingRequest struct {
	RunID       string
	Workspace   string
	Instruction string
	Environment map[string]string
	ReadOnly    bool
}
```
and
```go
type CodingAgent interface {
	Run(context.Context, CodingRequest, func(CodingProgress)) (CodingResult, error)
	Interrupt(string) error
}
```
both go away entirely, leaving just:
```go
type CodingProgress struct {
	Kind    string
	Message string
	RunID   string
}

type CodingResult struct {
	Summary       string
	Validation    string
	CommitMessage string
	ChangedFiles  []string
}
```
in that position of the file.

- [ ] **Step 5: Delete the CLI adapters and the CLI-agent runtime**

```bash
rm -rf internal/adapters/coding/claudecli internal/adapters/coding/codexcli
rm internal/kernel/services/coding_runtime.go internal/kernel/services/coding_runtime_test.go
```

- [ ] **Step 6: Update `internal/kernel/agent/prompt.go`**

Remove `ActiveCodingAgent` and `CodingAgentReady` from the struct:

```go
type CapabilityManifest struct {
	ActiveModel           string
	Repositories          []string
	Tools                 []string
	RepositoryCommitReady bool
	RepositoryPushReady   bool
	PullRequestReady      bool
	CalendarEnabled       bool
}
```

Update `renderCapabilityManifest`:

```go
func renderCapabilityManifest(capability CapabilityManifest) string {
	repositories := append([]string(nil), capability.Repositories...)
	tools := append([]string(nil), capability.Tools...)
	sort.Strings(repositories)
	sort.Strings(tools)
	return fmt.Sprintf("Capability manifest\nactive_model: %s\nrepositories: [%s]\ntools: [%s]\nrepository_commit_ready: %t\nrepository_push_ready: %t\npull_request_ready: %t\nshipping_approval_flow: commit -> push -> pull_request\ncalendar_enabled: %t",
		capability.ActiveModel, strings.Join(repositories, ", "), strings.Join(tools, ", "), capability.RepositoryCommitReady, capability.RepositoryPushReady, capability.PullRequestReady, capability.CalendarEnabled)
}
```

Update the last line of `hardRuntimePolicy` (the `coding_agent_ready` field it references no longer exists):

```go
- The repository_modify tool is granted only on turns whose message reads as an explicit implementation request (e.g. "implement X", "fix Y", or an explicit commit/PR/MR lifecycle phrase); ordinary conversation, including planning or clarifying questions, does not carry it. If repository_modify is missing this turn despite configured repositories, say so plainly and ask the owner to restate the request with explicit implementation language — never report it as a misconfiguration or failure.`
```

- [ ] **Step 7: Update `internal/kernel/agent/prompt_test.go`**

Find the test asserting on `ActiveCodingAgent`/`CodingAgentReady` (currently: `ActiveModel: "deepseek-pro", ActiveCodingAgent: "claude", Repositories: []string{"zeta", "eggy"}, Tools: []string{"status", "repository_list"}, CodingAgentReady: true,`) and remove `ActiveCodingAgent: "claude",` and `CodingAgentReady: true,` from the `CapabilityManifest{...}` literal, and remove any corresponding assertions in that test on `active_coding_agent:`/`coding_agent_ready:` substrings in the rendered output.

- [ ] **Step 8: Update `internal/kernel/services/repository_tools.go`**

The `repository_modify` tool's description mentions "the coding adapter"; update it to be adapter-neutral:

Change:
```go
	modify := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_modify", Description: "Use only for an explicit owner request to change a configured repository; runs the coding adapter and requests commit approval. When provider capabilities are ready, Eggy automatically chains separate push and pull-request approvals; never tell the owner to recover the temporary workspace manually", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["repository","instruction"],"additionalProperties":false}`),
	}}
```
to:
```go
	modify := repositoryTool{definition: ports.ToolDefinition{
		Name: "repository_modify", Description: "Use only for an explicit owner request to change a configured repository; runs the bounded implementation loop and requests commit approval. When provider capabilities are ready, Eggy automatically chains separate push and pull-request approvals; never tell the owner to recover the temporary workspace manually", Schema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string","minLength":1},"instruction":{"type":"string","minLength":1}},"required":["repository","instruction"],"additionalProperties":false}`),
	}}
```

- [ ] **Step 9: Rewrite the `NewApp` bootstrap wiring in `internal/bootstrap/app.go`**

Remove the `claudecli`/`codexcli` imports (currently lines 24-25).

Remove the `CodexExecutable`/`ClaudeExecutable` fields from `AppOptions` (currently lines 48-49).

Remove the `codingRuntime *services.CodingAgentRuntime` and `codingAliases []string` fields from `App` (currently lines 72-73). Add one field in their place: `implementationLoop *agent.Loop`.

Replace the body of `NewApp` from its start through the line that currently reads `app.conversation = services.NewConversationService(stateStore, 20)` with:

```go
func NewApp(config Config, secrets Secrets, options AppOptions) (*App, error) {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.TelegramBaseURL == "" {
		options.TelegramBaseURL = "https://api.telegram.org"
	}
	if options.GitHubAPIBase == "" {
		options.GitHubAPIBase = "https://api.github.com"
	}
	if options.GoogleAuthURL == "" {
		options.GoogleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if options.GoogleTokenURL == "" {
		options.GoogleTokenURL = "https://oauth2.googleapis.com/token"
	}
	if options.GoogleAPIBase == "" {
		options.GoogleAPIBase = "https://www.googleapis.com/calendar/v3"
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	timezone := strings.TrimSpace(config.Calendar.Timezone)
	if timezone == "" {
		timezone = config.Scheduler.QuietHours.Timezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load owner timezone: %w", err)
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return nil, err
	}
	statePath := filepath.Join(config.DataDir, "state.json")
	_, statErr := os.Stat(statePath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat state: %w", statErr)
	}
	stateStore := jsonfile.Open(statePath)
	contextStore := contextmarkdown.Open(config.DataDir, 64<<10)
	app := &App{config: config, store: stateStore, context: contextStore, scheduler: schedulerlocal.New(stateStore), now: options.Now, eventQueue: make(chan events.Event, 64), logger: options.Logger, timezone: timezone, location: location}
	if errors.Is(statErr, os.ErrNotExist) && len(config.Repositories) > 0 {
		seeded := map[string]ports.Repository{}
		for _, configured := range config.Repositories {
			seeded[configured.Name] = ports.Repository{Name: configured.Name, CloneURL: configured.CloneURL, BaseBranch: configured.BaseBranch, ProtectedBranches: configured.ProtectedBranches}
		}
		initial, err := stateStore.Load(context.Background())
		if err != nil {
			return nil, err
		}
		if _, err := stateStore.Update(context.Background(), initial.Version, func(state *ports.State) error {
			state.Repositories = seeded
			return nil
		}); err != nil {
			return nil, fmt.Errorf("seed first-boot repositories: %w", err)
		}
	}
	var telegramClient *telegram.Client
	if options.FakeAdapters {
		app.channel = noopChannel{}
	} else {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
		app.channel = telegramClient
	}
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute)
	allowedEnvironment := append([]string(nil), config.Runner.AllowedEnv...)
	allowedEnvironment = append(allowedEnvironment, "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT")
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	repositoryCapabilities := repositoryAdapter.RepositoryCapabilities()
	app.shipping = services.NewShippingService(stateStore, app.approvals, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryCapabilities)
	app.shipping.SetApprovalRequester(app.approvals)
	app.repositoriesService = services.NewRepositoriesService(stateStore, runner, repositoryAdapter, app.approvals, app.approvals, repositoryCapabilities, newRunID)
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{
		approvals.Commit:        app.shipping,
		approvals.Push:          app.shipping,
		approvals.CreatePR:      app.shipping,
		approvals.AddRepository: app.repositoriesService,
	}
	app.conversation = services.NewConversationService(stateStore, 20)
```

Everything from the original `aliases := make([]string, 0, len(config.ModelAliases))` line (building `targets`/`app.agentRuntime`) through the original `app.conversation = services.NewConversationService(stateStore, 20)` line stays as it was — that block doesn't reference anything coding-agent-related and is unaffected. Immediately after `app.agentRuntime = services.NewAgentRuntime(stateStore, config.Agent.DefaultModel, aliases)` (which sets up `targets` and `app.agentRuntime`), insert the new implementer wiring, replacing where `app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, app.codingRuntime, options.Now)` used to be constructed:

```go
	app.implementationLoop = agent.NewSelectedLoop(targets, services.NewImplementationTools(runner, repositoryAdapter), nil, 24)
	implementer := services.NewNativeImplementer(app.implementationLoop, func(ctx context.Context) (string, error) {
		return app.agentRuntime.SelectedModel(ctx)
	})
	app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, implementer, options.Now)
```

(`targets` is the `map[string]agent.ModelTarget` already built a few lines earlier in this same function for `app.loop`; reuse it as-is — both loops share the same configured models.)

Remove the `CommandService` literal's now-nonexistent fields. Change:
```go
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, codingRuntime: app.codingRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, defaultCodingAgent: config.Coding.DefaultAgent, configPath: options.ConfigPath, modelAliases: aliases, now: options.Now}
```
to:
```go
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, configPath: options.ConfigPath, modelAliases: aliases, now: options.Now}
```

Update `capabilityManifest`. Change:
```go
func (a *App) capabilityManifest(state ports.State, activeModel string) agent.CapabilityManifest {
	manifest := a.manifest
	manifest.ActiveModel = activeModel
	manifest.ActiveCodingAgent = state.Coding.SelectedAgent
	if manifest.ActiveCodingAgent == "" {
		manifest.ActiveCodingAgent = a.config.Coding.DefaultAgent
	}
	manifest.Repositories = repositoryNamesFromState(state)
	configured := len(manifest.Repositories) > 0
	available := false
	for _, alias := range a.codingAliases {
		if alias == manifest.ActiveCodingAgent {
			available = true
			break
		}
	}
	manifest.CodingAgentReady = configured && available
	manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
	manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
	manifest.PullRequestReady = configured && manifest.PullRequestReady
	return manifest
}
```
to:
```go
func (a *App) capabilityManifest(state ports.State, activeModel string) agent.CapabilityManifest {
	manifest := a.manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = repositoryNamesFromState(state)
	configured := len(manifest.Repositories) > 0
	manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
	manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
	manifest.PullRequestReady = configured && manifest.PullRequestReady
	return manifest
}
```

Also delete the `if manifest.ActiveCodingAgent == ""` defaulting and any remaining reference to `a.config.Coding` or `a.codingAliases` you find while compiling (the `readyLog.Do` block near `Ready()` builds an `integrations` slice using `a.codingAliases` — replace `integrations = append(integrations, a.codingAliases...)` with nothing, i.e. drop that line, keeping the `if len(state.Repositories) > 0 { integrations = append(integrations, "github") }` part).

- [ ] **Step 10: Update `internal/bootstrap/commands.go`**

Remove the `codingRuntime *services.CodingAgentRuntime` and `defaultCodingAgent string` fields from the `CommandService` struct.

Delete the entire `case "/coding_agent":` block.

Delete the entire `case "coding_agent":` block inside the `/config set` dispatch (the one calling `SetCodingAgent`).

Update the four usage strings that enumerate `coding_agent` as a `/config` option:
- `"Usage: /config get <coding|providers|models|calendar|path>|set <coding_agent|provider|model|calendar> ..."` → `"Usage: /config get <providers|models|calendar|path>|set <provider|model|calendar> ..."`
- `"Usage: /config set <coding_agent|provider|model|calendar> ..."` (appears twice) → `"Usage: /config set <provider|model|calendar> ..."`

- [ ] **Step 11: Update `internal/adapters/channels/telegram/commands.go`**

Remove the `{Name: "coding_agent", Description: "Show or change the active coding-agent alias"},` line from `Commands()`.

- [ ] **Step 12: Update `internal/bootstrap/commands_test.go`**

Delete `TestCommandCodingAgentListsSelectsAndResets`, `TestCommandCodingAgentReportsUnconfiguredRuntime`, and the `commandTestCodingAgent` type. In whichever test currently exercises `/config set coding_agent alias=claude adapter=claude_cli credential_env=CLAUDE_CODE_OAUTH_TOKEN`, remove that command invocation and its assertions (the `/config get coding` output check at the same call site goes too, since `coding` is no longer a `/config get` section — see Task 6).

- [ ] **Step 13: Update `internal/bootstrap/app_test.go`**

Delete `TestCodingAgentBootstrapPreservesCodexOnlyCompatibility`, `TestCodingAgentBootstrapRegistersCredentialReadyClaudeAndSwitchesGlobally`, `TestCodingAgentBootstrapSkipsClaudeWithoutOptionalCredential`, `TestCodingAgentBootstrapRejectsUnavailableDefault`, and `TestCodingAgentReadinessReportsAvailableAliasWithoutCredentials`. In any remaining test that builds a `CapabilityManifest` or asserts on `withoutRepository`/`withRepository` capability fields, remove the `.ActiveCodingAgent`/`.CodingAgentReady` assertions, keeping the `RepositoryCommitReady`/`RepositoryPushReady`/`PullRequestReady` ones.

- [ ] **Step 14: Build and fix remaining compile errors**

Run: `go build ./...`

This will surface any remaining reference this plan's instructions didn't explicitly name (for example, a stray `config.Coding` read left in `app.go`, or an unused import). Fix each one by deleting the offending line/import — every remaining reference at this point is to something this task intentionally removed, so there is no case where a fix here should be anything other than a deletion.

- [ ] **Step 15: Add an end-to-end acceptance test for the native path**

`internal/bootstrap/app_test.go` builds `*App` with `AppOptions.FakeAdapters = true` for its existing integration-style tests — find that pattern (search for `FakeAdapters: true` and how those tests inject a fake `ports.Model` and a fake `ports.CodingRepository`/GitHub adapter) and add one test proving the full native path, replacing whatever the old Codex/Claude-flow acceptance test asserted. The scenario it must prove, at minimum:

1. Configure one repository and a fake model whose queued responses are: (a) a `repository_modify` tool call for an "implement" message, then, once control reaches the implementation loop, (b) a `terminal` or `read_file` tool call followed by (c) a `finish_implementation` call with a non-empty `summary`/`commit_message`.
2. After the turn completes, assert: a fake `ports.CodingRepository`'s `CreateBranch` was called with `eggy/<run-id>`, `Diff` was called, and a commit `approvals.Approval` was delivered to the fake channel — i.e., the same observable shipping behavior the pre-refactor Codex-backed test asserted, now produced by `NativeImplementer` instead of a CLI subprocess.
3. Assert no CLI executable is invoked (there no longer is one — this is implicit once `claudecli`/`codexcli` are deleted, but worth a one-line comment noting why the test no longer stubs an executable path).

If an equivalent test already exists and only needs its fake model's queued tool calls updated to match the new tool names (`read_file`/`terminal`/`finish_implementation` instead of whatever it queued for the CLI path), update it in place rather than adding a duplicate.

- [ ] **Step 16: Run the full test suite**

Run: `go test ./... -race`
Expected: PASS. `go vet ./...` and `gofmt -l .` (should print nothing) too.

- [ ] **Step 17: Commit**

```bash
git add -A
git commit -m "refactor: replace Codex/Claude Code CLI coding agents with NativeImplementer"
```

---

## Task 6: Remove the coding-agent config surface

Decoupled from Task 5's compile requirements — Task 5 already stopped reading `config.Coding` in `app.go`/`commands.go`. This task deletes the now-dead schema and mutation code.

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `internal/bootstrap/config_mutate.go`
- Modify: `internal/bootstrap/config_mutate_test.go`

- [ ] **Step 1: Delete the now-unused config tests**

In `internal/bootstrap/config_test.go`, delete `TestCodingConfigDefaultsForVersion1AndOmittedVersion2`, `TestCodingConfigLoadsCredentialsWithoutPersistingThem`, `TestCodingConfigValidation`, and `TestCodingConfigRequiresOnlyDefaultAgentCredential`.

In `internal/bootstrap/config_mutate_test.go`, delete `TestSetCodingAgentAddsAndOverwritesEntry`, `TestSetCodingAgentRejectsInvalidAdapterAndLeavesFileUnchanged`, `TestSetCodingAgentRejectsVersion1Config`, and `TestSetCodingAgentSerializesConcurrentWrites`.

- [ ] **Step 2: Run the remaining tests to confirm they still pass without the deleted ones**

Run: `go test ./internal/bootstrap/... -v`
Expected: PASS (the deleted tests are gone; everything else still compiles and passes because Task 5 already removed the production code's dependency on `config.Coding`).

- [ ] **Step 3: Remove the config schema and mutation code**

In `internal/bootstrap/config.go`, delete:
- `type CodingConfig struct { ... }` and `type CodingAgentConfig struct { ... }`
- The `Coding CodingConfig` field from both `Config` and `configV2Document` (and the corresponding entries in the struct literals that build/copy them — the `Coding: ...` key in each composite literal referencing these types)
- `func defaultCodingConfig() CodingConfig { ... }` and every call to it
- The `if c.Coding.DefaultAgent == "" && len(c.Coding.Agents) == 0 { ... }`-shaped guards
- The coding-specific body of `func (c Config) validateCoding() error { ... }` and its call site in the aggregate validation function (if `validateCoding` becomes empty, delete the function and its call entirely)
- `CodingAgentCredentials map[string]string` from `Secrets`, its initialization (`CodingAgentCredentials: map[string]string{}`), and the loop that populates it from `cfg.Coding.Agents`
- The `if agent, ok := c.Coding.Agents[c.Coding.DefaultAgent]; ...` block in the required-secrets check

In `internal/bootstrap/config_mutate.go`, delete `func SetCodingAgent(...)` entirely, and the `lines = append(lines, "default_agent: "+cfg.Coding.DefaultAgent)`-shaped line(s) in whatever function renders `/config get coding` output (if that entire rendering function only existed to serve `coding`, delete the function and its dispatch case too — cross-check against Task 5 Step 10, which already removed the `/config get coding` command-level entry point; this step removes the now-orphaned renderer underneath it).

- [ ] **Step 4: Build and fix remaining compile errors**

Run: `go build ./...`
Fix any remaining reference to `CodingConfig`, `CodingAgentConfig`, `defaultCodingConfig`, `validateCoding`, `CodingAgentCredentials`, or `SetCodingAgent` by deleting it — same reasoning as Task 5 Step 14.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: remove the coding-agent config schema and mutation surface"
```

---

## Task 7: Documentation cleanup

**Files:**
- Modify: `README.md`
- Modify: `Dockerfile`

No production code depends on these; this task can land whenever after Task 6.

- [ ] **Step 1: Update `README.md`**

Remove:
- The "Install and authorize Codex locally" and "Claude Code is optional..." sections under "Local setup," including the `codex login --device-auth` and `claude setup-token` instructions and the `CODEX_HOME`/`CLAUDE_CONFIG_DIR`/`CLAUDE_CODE_OAUTH_TOKEN` guidance.
- The `/coding_agent` entries from the Telegram command list, and the `/config set coding_agent ...` example.
- The "Open a shell in the running service and authorize the persisted Codex home" step and the "To enable Claude Code instead of..." paragraph under Railway deployment.
- The sentence pinning `Codex CLI 0.144.5` and `Claude Code 2.1.215` and mentioning `CODEX_VERSION`/`CLAUDE_CODE_VERSION` build arguments.
- `Codex CLI and/or Claude Code CLI` from the "Requirements" line under "Local setup" — replace with a description matching the new architecture: repository editing now runs through the same configured model as conversation, no separate CLI install.

Update the overview paragraph (currently: "A configurable coding agent — Codex CLI or Claude Code, selectable with `/coding_agent` — owns editing, testing, and debugging.") to: "The same configured reasoning model owns editing, testing, and debugging, using its own `read_file`/`terminal`/`patch`/`write_file` tools inside an isolated branch."

- [ ] **Step 2: Update `Dockerfile`**

Remove the `CODEX_VERSION`/`CLAUDE_CODE_VERSION` build arguments and whatever `npm install -g @openai/codex @anthropic-ai/claude-code`-equivalent install steps exist for them. Read the file first to confirm exact line numbers before editing — this plan doesn't reproduce Dockerfile contents since none of the earlier research touched it.

- [ ] **Step 3: Commit**

```bash
git add README.md Dockerfile
git commit -m "docs: remove Codex/Claude Code CLI setup instructions"
```

---

## Task 8: Final verification

**Files:** none — verification only.

- [ ] **Step 1: Run the complete verification suite**

```bash
make fmt vet test race build
```

Expected: all pass, no diffs from `fmt`, no vet warnings, all tests green, race detector clean, binary builds.

- [ ] **Step 2: Run the Docker smoke test if Docker is available**

```bash
make smoke
```

Expected: builds the production image, starts `eggyd` with fake adapters, verifies `/healthz`/`/readyz`, removes the container. If Docker isn't available in this environment, note that explicitly rather than skipping silently.

- [ ] **Step 3: Manually confirm the acceptance shape**

Using `EGGY_CONFIG` pointed at a config with `FakeAdapters` (or a real DeepSeek key and a disposable repository), confirm: a read-only question about the repo answers via `read_file`/`terminal` without creating a branch; an explicit "implement X" request creates an `eggy/<run-id>` branch, produces a diff, and requests commit approval; `/coding_agent` no longer exists as a command; `/config get coding` no longer exists as a subcommand.

- [ ] **Step 4: Commit if Step 3 uncovered any fixes**

```bash
git add -A
git commit -m "fix: address issues found during manual verification"
```

(Only if there were fixes — otherwise nothing to commit here.)
