package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestClaudeRunUsesStreamJSONEnvironmentAndNormalizesProgress(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"session-1"}`,
		`not-json`,
		`{"type":"system","subtype":"api_retry","attempt":1,"error":"overloaded"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working"},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"result","subtype":"success","result":"{\"summary\":\"Implemented and tested.\",\"validation\":\"go test ./... passed\",\"commit_message\":\"feat: implement change\",\"changed_files\":[\"main.go\"]}"}`,
	}, "\n")}}
	adapter := New("/usr/local/bin/claude", runner, 4096, "oauth-secret", "/data/claude")
	var progress []ports.CodingProgress
	result, err := adapter.Run(context.Background(), ports.CodingRequest{
		RunID: "run-1", Workspace: t.TempDir(), Instruction: "fix tests",
		Environment: map[string]string{"UNTRUSTED": "ignored"},
	}, func(update ports.CodingProgress) { progress = append(progress, update) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Implemented and tested." || result.Validation != "go test ./... passed" || result.CommitMessage != "feat: implement change" || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "main.go" {
		t.Fatalf("result=%#v", result)
	}
	wantArgv := []string{"/usr/local/bin/claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "fix tests"}
	if strings.Join(runner.command.Argv, "\x00") != strings.Join(wantArgv, "\x00") || runner.command.Dir == "" || runner.command.MaxOutput != 4096 {
		t.Fatalf("command=%#v", runner.command)
	}
	if len(runner.command.Env) != 2 || runner.command.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-secret" || runner.command.Env["CLAUDE_CONFIG_DIR"] != "/data/claude" {
		t.Fatalf("environment keys=%v", environmentKeys(runner.command.Env))
	}
	if !runner.streamed {
		t.Fatal("Claude did not use streaming runner")
	}
	wantKinds := []string{"started", "diagnostic", "diagnostic", "tool", "completed"}
	if got := progressKinds(progress); strings.Join(got, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("progress=%#v", progress)
	}
	for _, update := range progress {
		if len(update.Message) > maxProgressMessage {
			t.Fatalf("unbounded progress=%#v", update)
		}
		if strings.Contains(update.Message, "oauth-secret") {
			t.Fatalf("credential leaked in progress=%#v", update)
		}
	}
}

func TestClaudeReadOnlyUsesPlanAndDoesNotRequireCommitMessage(t *testing.T) {
	runner := &fakeRunner{result: ports.CommandResult{Stdout: `{"type":"result","subtype":"success","result":"{\"summary\":\"inspected\",\"validation\":\"read only\",\"changed_files\":[]}"}`}}
	adapter := New("claude", runner, 4096, "token", "/data/claude")
	if _, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "inspect-1", Workspace: t.TempDir(), Instruction: "inspect", ReadOnly: true}, nil); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(runner.command.Argv, " "); !strings.Contains(joined, "--permission-mode plan") || strings.Contains(joined, "bypassPermissions") {
		t.Fatalf("argv=%v", runner.command.Argv)
	}
}

func TestClaudeRejectsMalformedOrIncompleteResult(t *testing.T) {
	for _, test := range []struct {
		name, output, want string
	}{
		{name: "missing", output: `{"type":"system","subtype":"init"}`, want: "final result"},
		{name: "malformed", output: `{"type":"result","result":"not-json"}`, want: "structured result"},
		{name: "summary", output: `{"type":"result","result":"{\"summary\":\"\",\"commit_message\":\"feat: x\"}"}`, want: "summary"},
		{name: "commit message", output: `{"type":"result","result":"{\"summary\":\"done\"}"}`, want: "commit_message"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{result: ports.CommandResult{Stdout: test.output}}
			adapter := New("claude", runner, 4096, "token", "/data/claude")
			_, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: test.name, Workspace: t.TempDir(), Instruction: "change it"}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestClaudeRedactsCredentialFromErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		runner *fakeRunner
	}{
		{name: "runner", runner: &fakeRunner{err: errors.New("authentication failed for oauth-secret")}},
		{name: "stream", runner: &fakeRunner{result: ports.CommandResult{Stdout: `{"type":"error","message":"authentication failed for oauth-secret"}`}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := New("claude", test.runner, 4096, "oauth-secret", "/data/claude")
			var progress []ports.CodingProgress
			_, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: t.TempDir(), Instruction: "change it"}, func(update ports.CodingProgress) { progress = append(progress, update) })
			if err == nil || strings.Contains(err.Error(), "oauth-secret") || !strings.Contains(err.Error(), "Claude Code") {
				t.Fatalf("error=%v", err)
			}
			for _, update := range progress {
				if strings.Contains(update.Message, "oauth-secret") {
					t.Fatalf("credential leaked in progress=%#v", update)
				}
			}
			if test.name == "stream" && strings.Join(progressKinds(progress), ",") != "error" {
				t.Fatalf("progress=%#v", progress)
			}
		})
	}
}

func TestClaudeInterruptCancelsActiveRun(t *testing.T) {
	runner := &fakeRunner{block: make(chan struct{}), started: make(chan struct{})}
	adapter := New("claude", runner, 1024, "token", "/data/claude")
	done := make(chan error, 1)
	go func() {
		_, err := adapter.Run(context.Background(), ports.CodingRequest{RunID: "run-1", Workspace: t.TempDir(), Instruction: "wait"}, nil)
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

func environmentKeys(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	return keys
}

func progressKinds(progress []ports.CodingProgress) []string {
	kinds := make([]string, 0, len(progress))
	for _, update := range progress {
		kinds = append(kinds, update.Kind)
	}
	return kinds
}
