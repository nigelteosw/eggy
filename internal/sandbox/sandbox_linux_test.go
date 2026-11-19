//go:build linux

package sandbox

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary be the sandbox-exec entry point, as eggyd is:
// the sandbox re-runs os.Executable, which here is this binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(ExecMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// requireSandbox skips unless this process is root and the coder user exists.
// Skipping is reported as skipped, never as a pass.
func requireSandbox(t *testing.T) (*Sandbox, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("sandbox tests need root and the coder user")
	}
	sb, err := New()
	if err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	dataDir := t.TempDir()
	// The coder user must be able to traverse to its run directories: the data
	// directory, every temp directory above it (Go makes those 0700), and
	// agent-runs, as LockDataDir leaves them in production.
	for dir := dataDir; dir != os.TempDir() && dir != "/"; dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dataDir, "agent-runs"), 0o711); err != nil {
		t.Fatal(err)
	}
	return sb, dataDir
}

func runIn(t *testing.T, sb *Sandbox, dataDir string, argv ...string) Result {
	t.Helper()
	run := filepath.Join(dataDir, "agent-runs", "t-"+strings.ReplaceAll(t.Name(), "/", "-"))
	if err := sb.PrepareRunDir(run); err != nil {
		t.Fatal(err)
	}
	result, err := sb.Run(context.Background(), Spec{RunDir: run, Argv: argv})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSandboxProbePasses(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	if err := sb.Probe(context.Background(), dataDir, 1); err != nil {
		t.Fatalf("probe failed on a working sandbox: %v", err)
	}
}

func TestSandboxCannotReadOutsideRunDir(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	secret := filepath.Join(dataDir, "secret")
	if err := os.WriteFile(secret, []byte("root-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runIn(t, sb, dataDir, "cat", secret)
	if result.ExitCode == 0 || strings.Contains(result.Output, "root-only") {
		t.Fatalf("read a root-only file: exit %d output %q", result.ExitCode, result.Output)
	}
}

func TestSandboxCannotConnectToNonWebPort(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	result := runIn(t, sb, dataDir, "bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+strconv.Itoa(port))
	if result.ExitCode == 0 {
		t.Fatal("connected to a non-web port")
	}
}

func TestSandboxEnvHasNoParentSecrets(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	t.Setenv("EGGY_TEST_SECRET", "s3cr3t")
	result := runIn(t, sb, dataDir, "env")
	if strings.Contains(result.Output, "s3cr3t") {
		t.Fatalf("secret reached the command: %s", result.Output)
	}
}

func TestSandboxTimeoutKillsGroup(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	run := filepath.Join(dataDir, "agent-runs", "timeout")
	if err := sb.PrepareRunDir(run); err != nil {
		t.Fatal(err)
	}
	result, err := sb.Run(context.Background(), Spec{RunDir: run, Argv: []string{"bash", "-c", "sleep 100 & sleep 100"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut {
		t.Fatalf("expected timeout, got exit %d", result.ExitCode)
	}
}

// A backgrounded child must not outlive its command: it could keep writing into
// the workspace while the publisher reads it. The child here would leave a
// marker two seconds after the command returns, so the marker must not appear.
func TestSandboxKillsBackgroundChildren(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	run := filepath.Join(dataDir, "agent-runs", "background")
	if err := sb.PrepareRunDir(run); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(run, "ws", "marker")
	if _, err := sb.Run(context.Background(), Spec{RunDir: run, Argv: []string{"bash", "-c", "(sleep 2; touch marker) &"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a background child outlived its command and wrote into the workspace")
	}
}

// Terminal mode opens a pty and reads randomness, and still cannot read a
// root-only file. Without the device rules herdr-core panics at startup.
func TestTerminalModeOpensPTYAndStaysConfined(t *testing.T) {
	sb, dataDir := requireSandbox(t)
	dir := filepath.Join(dataDir, TerminalDir)
	if err := sb.PrepareRunDir(dir); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dataDir, "secret")
	if err := os.WriteFile(secret, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(script string) (string, error) {
		cmd, err := sb.Terminal(dir, []string{"bash", "-c", script})
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("exec 3<>/dev/ptmx && head -c1 /dev/urandom >/dev/null && echo ok"); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("terminal mode cannot open a pty: %v: %s", err, out)
	}
	if out, err := run("cat " + secret); err == nil || strings.Contains(out, "s3cr3t") {
		t.Fatalf("terminal mode read a root-only file: %s", out)
	}
	if _, err := run("exec 3<>/dev/ptmx"); err != nil {
		t.Fatal(err)
	}
	// Command mode keeps no pty access: the agent runner's rules do not widen.
	result, err := sb.Run(context.Background(), Spec{RunDir: dir, Argv: []string{"bash", "-c", "exec 3<>/dev/ptmx"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == 0 {
		t.Fatal("command mode opened a pty")
	}
}
