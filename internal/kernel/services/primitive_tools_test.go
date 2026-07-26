package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// stubWorkspaces resolves a fixed binding, standing in for whatever session
// state (implementation run or attached thread checkout) supplied it.
type stubWorkspaces struct {
	binding WorkspaceBinding
	err     error
}

func (s stubWorkspaces) Resolve(context.Context) (WorkspaceBinding, error) {
	return s.binding, s.err
}

func primitivesByName(tools []ports.Tool) map[string]ports.Tool {
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	return byName
}

func writablePrimitives(t *testing.T, runner ports.Runner, reader ports.RepositoryReader, dir string) map[string]ports.Tool {
	t.Helper()
	return primitivesByName(NewPrimitiveTools(stubWorkspaces{binding: WorkspaceBinding{Path: dir, Writable: true}}, runner, reader))
}

func TestPrimitivesRequireAnAttachedWorkspace(t *testing.T) {
	byName := primitivesByName(NewPrimitiveTools(stubWorkspaces{err: ErrNoWorkspace}, &fakeWorkspaceRunner{}, &fakeRepositoryReader{content: "line one\n"}))
	for _, name := range PrimitiveNames {
		input := map[string]json.RawMessage{
			"read_file":  json.RawMessage(`{"path":"main.go"}`),
			"terminal":   json.RawMessage(`{"command":"ls"}`),
			"patch":      json.RawMessage(`{"path":"main.go","old_string":"a","new_string":"b"}`),
			"write_file": json.RawMessage(`{"path":"main.go","content":"a"}`),
		}[name]
		if _, err := byName[name].Execute(context.Background(), input); !errors.Is(err, ErrNoWorkspace) {
			t.Fatalf("%s: err=%v, want ErrNoWorkspace", name, err)
		}
	}
}

func TestPrimitivesTakeNoRepositoryArgument(t *testing.T) {
	byName := writablePrimitives(t, &recordingRunner{}, &fakeRepositoryReader{}, t.TempDir())
	for _, name := range PrimitiveNames {
		schema := string(byName[name].Definition().Schema)
		if strings.Contains(schema, `"repository"`) {
			t.Fatalf("%s resolves its workspace from session state, so its schema must not take a repository: %s", name, schema)
		}
	}
}

func TestWritePrimitivesStayRegisteredAndFailOnAReadOnlyWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("func old() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := NewPrimitiveTools(stubWorkspaces{binding: WorkspaceBinding{Path: dir}}, &recordingRunner{result: ports.CommandResult{Stdout: "main.go\n"}}, &fakeRepositoryReader{content: "func old() {}\n"})
	byName := primitivesByName(tools)

	// Gating is by result, not by registry membership: every primitive is
	// present, and only the writes refuse.
	for _, name := range PrimitiveNames {
		if byName[name] == nil {
			t.Fatalf("%s must stay registered on a read-only workspace", name)
		}
	}
	if _, err := byName["patch"].Execute(context.Background(), json.RawMessage(`{"path":"main.go","old_string":"func old()","new_string":"func new()"}`)); !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Fatalf("patch err=%v, want ErrWorkspaceReadOnly", err)
	}
	if _, err := byName["write_file"].Execute(context.Background(), json.RawMessage(`{"path":"new.go","content":"package pkg\n"}`)); !errors.Is(err, ErrWorkspaceReadOnly) {
		t.Fatalf("write_file err=%v, want ErrWorkspaceReadOnly", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.go")); !os.IsNotExist(err) {
		t.Fatal("a refused write must not touch the workspace")
	}
	if _, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"path":"main.go"}`)); err != nil {
		t.Fatalf("read_file must still work read-only: %v", err)
	}
	if _, err := byName["terminal"].Execute(context.Background(), json.RawMessage(`{"command":"ls"}`)); err != nil {
		t.Fatalf("terminal must still work read-only: %v", err)
	}
}

func TestReadFileReadsFromTheResolvedWorkspace(t *testing.T) {
	byName := writablePrimitives(t, &fakeWorkspaceRunner{}, &fakeRepositoryReader{content: "line one\n"}, "/tmp/run-1")
	result, err := byName["read_file"].Execute(context.Background(), json.RawMessage(`{"path":"main.go"}`))
	if err != nil || !strings.Contains(string(result), "line one") {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func TestTerminalRunsInTheResolvedWorkspace(t *testing.T) {
	runner := &recordingRunner{result: ports.CommandResult{Stdout: "README.md\n"}}
	byName := writablePrimitives(t, runner, &fakeRepositoryReader{}, "/tmp/run-1")
	result, err := byName["terminal"].Execute(context.Background(), json.RawMessage(`{"command":"ls"}`))
	if err != nil || !strings.Contains(string(result), "README.md") {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if runner.command.Dir != "/tmp/run-1" || runner.command.Argv[0] != "sh" || runner.command.Argv[2] != "ls" {
		t.Fatalf("command=%#v", runner.command)
	}
}

func TestPatchReplacesUniqueMatchAndRejectsAmbiguousOrMissingMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc old() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	byName := writablePrimitives(t, &fakeWorkspaceRunner{}, &fakeRepositoryReader{}, dir)

	if _, err := byName["patch"].Execute(context.Background(), json.RawMessage(`{"path":"main.go","old_string":"func old()","new_string":"func new()"}`)); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "func new()") {
		t.Fatalf("updated=%s", updated)
	}

	if _, err := byName["patch"].Execute(context.Background(), json.RawMessage(`{"path":"main.go","old_string":"func missing()","new_string":"x"}`)); err == nil {
		t.Fatal("expected not-found error")
	}
	if err := os.WriteFile(path, []byte("func a() {}\nfunc a() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := byName["patch"].Execute(context.Background(), json.RawMessage(`{"path":"main.go","old_string":"func a() {}","new_string":"x"}`)); err == nil {
		t.Fatal("expected ambiguous-match error")
	}
}

func TestWritePrimitivesRejectPathsEscapingTheWorkspace(t *testing.T) {
	byName := writablePrimitives(t, &fakeWorkspaceRunner{}, &fakeRepositoryReader{}, t.TempDir())
	if _, err := byName["patch"].Execute(context.Background(), json.RawMessage(`{"path":"../../etc/passwd","old_string":"a","new_string":"b"}`)); !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Fatalf("patch err=%v", err)
	}
	if _, err := byName["write_file"].Execute(context.Background(), json.RawMessage(`{"path":"../escaped.go","content":"x"}`)); !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Fatalf("write_file err=%v", err)
	}
}

func TestWriteFileCreatesFileAndParentDirectories(t *testing.T) {
	dir := t.TempDir()
	byName := writablePrimitives(t, &fakeWorkspaceRunner{}, &fakeRepositoryReader{}, dir)
	if _, err := byName["write_file"].Execute(context.Background(), json.RawMessage(`{"path":"pkg/new.go","content":"package pkg\n"}`)); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "pkg/new.go"))
	if err != nil || string(content) != "package pkg\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
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
