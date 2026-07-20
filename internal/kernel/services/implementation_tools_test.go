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
