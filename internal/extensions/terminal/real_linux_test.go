//go:build linux

package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/sandbox"
)

// TestMain lets this binary be the sandbox-exec entry point, as eggyd is.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.ExecMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// TestRealHerdrCore runs the service against the real hc binary, confined by
// the real sandbox. It needs root, the coder user and hc on the sandbox PATH,
// so it runs in the image (make docker-test) and skips elsewhere.
func TestRealHerdrCore(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("/usr/bin/hc"); err != nil {
		t.Skip("needs hc at /usr/bin/hc")
	}
	box, err := sandbox.New()
	if err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	root := t.TempDir()
	for dir := root; dir != os.TempDir() && dir != "/"; dir = filepath.Dir(dir) {
		_ = os.Chmod(dir, 0o711)
	}
	svc, err := New(Options{
		Root:     root,
		DirName:  func(a string) string { return "acct-" + a },
		Prepare:  box.PrepareAccountDir,
		Launcher: SandboxLauncher{Start: box.Terminal},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := as("alice")
	if err := svc.CreateSpace(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	term, err := svc.Start(ctx, "web", "shell", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// A second shell in the same space: hc names must be unique, labels need not be.
	second, err := svc.Start(ctx, "web", "shell", 80, 24)
	if err != nil {
		t.Fatalf("second shell: %v", err)
	}
	defer func() { _ = svc.Kill(ctx, second.Address) }()
	stream, err := svc.Attach(ctx, term.Address, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// Status the way the Claude Code hook reports it, then a marker to wait on.
	_, _ = stream.Write([]byte(`printf '\033]7501;state=blocked:app=test\033\\'; echo MARK$((40+2)); pwd` + "\r"))
	var seen strings.Builder
	deadline := time.After(15 * time.Second)
	for !strings.Contains(seen.String(), "MARK42") {
		select {
		case chunk, ok := <-stream.Output():
			if !ok {
				t.Fatalf("stream ended: %s; output %q", stream.Reason(), seen.String())
			}
			seen.Write(chunk)
			_ = stream.Consumed(len(chunk))
		case <-deadline:
			t.Fatalf("no marker; output %q", seen.String())
		}
	}
	if !strings.Contains(seen.String(), filepath.Join("acct-alice", "ws", "web")) {
		t.Fatalf("shell is not in its space: %q", seen.String())
	}
	eventually(t, "blocked status from OSC 7501", func() bool {
		ts, _ := svc.NeedsYou(ctx)
		return len(ts) == 1 && ts[0].App == "test"
	})
	_ = stream.Close()

	// A new service, as after an in-process restart, finds the same terminal.
	_ = svc.Close()
	again, _ := New(Options{Root: root, DirName: func(a string) string { return "acct-" + a }, Prepare: box.PrepareAccountDir, Launcher: SandboxLauncher{Start: box.Terminal}})
	defer again.Close()
	ts, err := again.Terminals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Address != term.Address || !ts[0].Live || ts[0].Name != "shell" {
		t.Fatalf("after reconnect: %+v", ts)
	}
	settings, err := os.ReadFile(filepath.Join(root, "acct-alice", "home", ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), "state=blocked") {
		t.Fatalf("status hooks: %v %s", err, settings)
	}
	if err := again.Kill(ctx, term.Address); err != nil {
		t.Fatal(err)
	}
}
