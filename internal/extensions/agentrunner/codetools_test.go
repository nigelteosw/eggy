package agentrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func findTool(t *testing.T, tools []ports.Tool, name string) ports.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Definition().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not offered", name)
	return nil
}

func call(t *testing.T, tool ports.Tool, input string) (string, error) {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		return "", err
	}
	var result struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(out, &result)
	return result.Output, nil
}

// newWorkspaceForTools returns a workspace whose ws/ exists, and the run.
func newWorkspaceForTools(t *testing.T, runner commandRunner, checks [][]string) (*Workspaces, *ActiveRun, string) {
	t.Helper()
	w := NewWorkspaces(t.TempDir(), runner)
	if err := makeRunDirs(w.Dir(runIDA)); err != nil {
		t.Fatal(err)
	}
	return w, &ActiveRun{ID: runIDA, Checks: checks}, filepath.Join(w.Dir(runIDA), "ws")
}

func TestEditExactUnique(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	tools := CodingTools(w, run, true)
	edit := findTool(t, tools, "edit")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, edit, `{"path":"a.txt","old":"x","new":"y"}`); err == nil {
		t.Fatal("two matches were replaced")
	}
	if _, err := call(t, edit, `{"path":"a.txt","old":"nope","new":"y"}`); err == nil {
		t.Fatal("zero matches were accepted")
	}
	if _, err := call(t, edit, `{"path":"a.txt","old":"","new":"z"}`); err == nil {
		t.Fatal("create over an existing file was accepted")
	}
	if _, err := call(t, edit, `{"path":"new.txt","old":"","new":"hello"}`); err != nil {
		t.Fatalf("create refused: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(ws, "new.txt"))
	if string(body) != "hello" {
		t.Fatalf("created file = %q", body)
	}
}

func TestRunCheckRecordsAndBashDoesNot(t *testing.T) {
	runner := &fakeRunner{result: SandboxResult{ExitCode: 0, Output: "ok"}}
	w, run, _ := newWorkspaceForTools(t, runner, [][]string{{"go", "test", "./..."}})
	tools := CodingTools(w, run, true)
	if _, err := call(t, findTool(t, tools, "bash"), `{"command":"echo hi"}`); err != nil {
		t.Fatal(err)
	}
	if len(run.CheckResults) != 0 {
		t.Fatalf("bash recorded a check result: %+v", run.CheckResults)
	}
	if _, err := call(t, findTool(t, tools, "run_check"), `{"index":0}`); err != nil {
		t.Fatal(err)
	}
	if len(run.CheckResults) != 1 || !slices.Equal(run.CheckResults[0].Argv, []string{"go", "test", "./..."}) || run.CheckResults[0].ExitCode != 0 {
		t.Fatalf("check results = %+v", run.CheckResults)
	}
}

func TestBashUsesSandboxWithWorkspaceCommand(t *testing.T) {
	runner := &fakeRunner{}
	w, run, _ := newWorkspaceForTools(t, runner, nil)
	if _, err := call(t, findTool(t, CodingTools(w, run, true), "bash"), `{"command":"echo hi"}`); err != nil {
		t.Fatal(err)
	}
	spec := runner.specs[0]
	if !slices.Equal(spec.Argv, []string{"bash", "-c", "echo hi"}) || spec.RunDir != w.Dir(runIDA) {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestRunCheckRejectsOutOfRangeIndex(t *testing.T) {
	w, run, _ := newWorkspaceForTools(t, &fakeRunner{}, [][]string{{"go", "vet"}})
	tool := findTool(t, CodingTools(w, run, true), "run_check")
	for _, input := range []string{`{"index":1}`, `{"index":-1}`} {
		if _, err := call(t, tool, input); err == nil {
			t.Fatalf("%s accepted", input)
		}
	}
}

func TestNoBashWithoutSandbox(t *testing.T) {
	w, run, _ := newWorkspaceForTools(t, &fakeRunner{}, nil)
	for _, tool := range CodingTools(w, run, false) {
		if tool.Definition().Name == "bash" {
			t.Fatal("bash offered without a passing sandbox probe")
		}
	}
}

func TestSearchFindsMatchesAndSkipsGit(t *testing.T) {
	w, run, ws := newWorkspaceForTools(t, &fakeRunner{}, nil)
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("needle"), 0o600)
	_ = os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n// needle\n"), 0o600)
	out, err := call(t, findTool(t, CodingTools(w, run, true), "search"), `{"pattern":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go:2") || strings.Contains(out, ".git") {
		t.Fatalf("search output = %q", out)
	}
}
