//go:build linux

package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	{
		c, _ := svc.coreFor(ctx)
		if err := c.run(ctx, "sh", "-c", "echo boom >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("a failing exec was not reported: %v", err)
		}
		if err := c.run(ctx, "true"); err != nil {
			t.Fatal(err)
		}
	}
	// A second shell in the same space: hc names must be unique, labels need not be.
	second, err := svc.Start(ctx, "web", "shell", 80, 24)
	if err != nil {
		t.Fatalf("second shell: %v", err)
	}
	defer func() { _ = svc.Kill(ctx, second.Address) }()
	shellInRun(t, svc, ctx, filepath.Join(root, "acct-alice"))
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

// shellInRun opens a shell in a run's checkout under the account directory,
// laid out as the workspace lays it out (runs/ and the run root's, the
// checkout the account's), and proves it works there by making a file.
func shellInRun(t *testing.T, svc *Service, ctx context.Context, account string) {
	t.Helper()
	id := strings.Repeat("e", 32)
	checkout := filepath.Join(account, "runs", id, "ws")
	if err := os.MkdirAll(checkout, 0o711); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(account, "home"))
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if err := os.Chown(checkout, int(st.Uid), int(st.Gid)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	svc.opts.Runs = func(context.Context) ([]Run, error) {
		return []Run{{ID: id, Repository: "web", State: "waiting_for_user"}}, nil
	}
	term, err := svc.StartInRun(ctx, id, "shell", 80, 24)
	if err != nil {
		t.Fatalf("shell in a run: %v", err)
	}
	defer func() { _ = svc.Kill(ctx, term.Address) }()
	stream, err := svc.Attach(ctx, term.Address, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	go func() {
		for range stream.Output() {
		}
	}()
	_, _ = stream.Write([]byte("touch ran-here\r"))
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(checkout, "ran-here")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell did not run in the run's checkout")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
