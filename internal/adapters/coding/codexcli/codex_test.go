package codexcli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodexRunUsesJSONWorkspaceSandboxAndNormalizesProgress(t *testing.T) {
	workspace := t.TempDir()
	runner := &fakeRunner{result: ports.CommandResult{Stdout: strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`not-json`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"ok","exit_code":0}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"{\"summary\":\"Implemented and tested.\",\"validation\":\"go test ./... passed\",\"commit_message\":\"feat: implement change\",\"changed_files\":[\"main.go\"]}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":4}}`,
	}, "\n")}}
	adapter := New("/usr/local/bin/codex", runner, 4096, "/data/codex")
	var progress []ports.CodingProgress
	result, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: workspace, Instruction: "fix tests", Environment: map[string]string{"UNTRUSTED": "ignored"}}, func(update ports.CodingProgress) { progress = append(progress, update) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Implemented and tested." || result.CommitMessage != "feat: implement change" || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "main.go" || !strings.Contains(result.Validation, "go test ./...") {
		t.Fatalf("result=%#v", result)
	}
	command := runner.command
	joined := strings.Join(command.Argv, " ")
	if !strings.Contains(joined, "exec --json") || !strings.Contains(joined, "--sandbox workspace-write") || !strings.Contains(joined, "--output-schema") || command.Dir != workspace || len(command.Env) != 1 || command.Env["CODEX_HOME"] != "/data/codex" {
		t.Fatalf("command=%#v", command)
	}
	if !strings.Contains(runner.schema, `"commit_message"`) || !strings.Contains(runner.schema, `"changed_files"`) {
		t.Fatalf("schema=%s", runner.schema)
	}
	if len(progress) < 3 {
		t.Fatalf("progress=%#v", progress)
	}
	if !runner.streamed {
		t.Fatal("Codex did not use streaming runner")
	}
}

func TestAdapterUsesReadOnlySandboxForInspection(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"{\"summary\":\"inspected\",\"validation\":\"read only\",\"commit_message\":\"chore: no changes\",\"changed_files\":[]}"}}`}}
	adapter := New("codex", runner, 4096, "/data/codex")
	if _, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "inspect-1", Workspace: t.TempDir(), Instruction: "inspect", ReadOnly: true}, nil); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(runner.command.Argv, " "); !strings.Contains(joined, "--sandbox read-only") || strings.Contains(joined, "danger-full-access") {
		t.Fatalf("argv=%v", runner.command.Argv)
	}
}

func TestAdapterRejectsUnstructuredFinalMessage(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"The branch and commit are ready"}}`}}
	adapter := New("codex", runner, 4096, "/data/codex")
	if _, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: t.TempDir(), Instruction: "change it"}, nil); err == nil || !strings.Contains(err.Error(), "structured") {
		t.Fatalf("error=%v", err)
	}
}

func TestCodexInterruptCancelsActiveRun(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), started: make(chan struct{})}
	adapter := New("codex", runner, 1024, "/data/codex")
	done := make(chan error, 1)
	workspace := t.TempDir()
	go func() {
		_, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: workspace, Instruction: "wait"}, nil)
		done <- err
	}()
	<-runner.started
	if err := adapter.Interrupt("run-1"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error=%v", err)
	}
	if err := adapter.Interrupt("missing"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}

type fakeRunner struct {
	command  ports.Command
	result   ports.CommandResult
	err      error
	block    chan struct{}
	started  chan struct{}
	streamed bool
	schema   string
}

func (r *fakeRunner) ExecuteStreaming(ctx context.Context, command ports.Command, line func(string)) (ports.CommandResult, error) {
	r.streamed = true
	result, err := r.Execute(ctx, command)
	if err == nil && line != nil {
		for _, value := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
			line(value)
		}
	}
	return result, err
}

func (r *fakeRunner) Create(context.Context, string) (string, error) { return "", nil }
func (r *fakeRunner) Destroy(context.Context, string) error          { return nil }
func (r *fakeRunner) Execute(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	r.command = command
	for index, argument := range command.Argv {
		if argument == "--output-schema" && index+1 < len(command.Argv) {
			data, _ := os.ReadFile(command.Argv[index+1])
			r.schema = string(data)
		}
	}
	if r.block != nil {
		close(r.started)
		select {
		case <-ctx.Done():
			return ports.CommandResult{}, ctx.Err()
		case <-r.block:
		}
	}
	return r.result, r.err
}
