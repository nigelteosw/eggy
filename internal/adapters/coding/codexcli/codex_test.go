package codexcli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodexRunUsesJSONWorkspaceSandboxAndNormalizesProgress(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`not-json`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"ok","exit_code":0}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Implemented and tested."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":4}}`,
	}, "\n")}}
	adapter := New("/usr/local/bin/codex", runner, 4096)
	var progress []ports.CodingProgress
	result, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: "/tmp/runs/run-1", Instruction: "fix tests", Environment: map[string]string{"CODEX_HOME": "/data/codex"}}, func(update ports.CodingProgress) { progress = append(progress, update) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Implemented and tested." || !strings.Contains(result.Validation, "go test ./...") {
		t.Fatalf("result=%#v", result)
	}
	command := runner.command
	joined := strings.Join(command.Argv, " ")
	if !strings.Contains(joined, "exec --json") || !strings.Contains(joined, "--sandbox workspace-write") || command.Dir != "/tmp/runs/run-1" || command.Env["CODEX_HOME"] != "/data/codex" {
		t.Fatalf("command=%#v", command)
	}
	if len(progress) < 3 {
		t.Fatalf("progress=%#v", progress)
	}
	if !runner.streamed {
		t.Fatal("Codex did not use streaming runner")
	}
}

func TestAdapterUsesReadOnlySandbox(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"inspected"}}`}}
	adapter := New("codex", runner, 4096)
	if _, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "inspect-1", Workspace: "/tmp/inspect", Instruction: "inspect", ReadOnly: true}, nil); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(runner.command.Argv, " "); !strings.Contains(joined, "--sandbox read-only") {
		t.Fatalf("argv=%v", runner.command.Argv)
	}
}

func TestCodexInterruptCancelsActiveRun(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), started: make(chan struct{})}
	adapter := New("codex", runner, 1024)
	done := make(chan error, 1)
	go func() {
		_, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: "/tmp/run", Instruction: "wait"}, nil)
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
