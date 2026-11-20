package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunEnvHasNoParentSecrets(t *testing.T) {
	t.Setenv("EGGY_TEST_SECRET", "s3cr3t")
	env := runEnv(filepath.Join(t.TempDir(), "run"))
	for _, entry := range env {
		if strings.Contains(entry, "s3cr3t") || strings.HasPrefix(entry, "EGGY_") {
			t.Fatalf("sandbox environment inherits %q", entry)
		}
	}
}

func TestRunEnvPointsIntoRunDir(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	env := runEnv(run)
	for _, want := range []string{"HOME=" + filepath.Join(run, "home"), "TMPDIR=" + filepath.Join(run, "tmp"), "GIT_CONFIG_NOSYSTEM=1"} {
		if !slices.Contains(env, want) {
			t.Fatalf("environment lacks %q: %v", want, env)
		}
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_CONFIG_GLOBAL=") && !strings.HasPrefix(entry, "GIT_CONFIG_GLOBAL="+run) {
			t.Fatalf("git config is not run-local: %q", entry)
		}
	}
}

func TestParseSandboxExec(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		ok       bool
		terminal bool
	}{
		{"valid", []string{"/data/agent-runs/x", "--", "bash", "-c", "ls"}, true, false},
		{"terminal", []string{"--terminal", "/data/terminal", "--", "hc", "connect"}, true, true},
		{"terminal flag after dir", []string{"/data/terminal", "--terminal", "--", "hc"}, false, false},
		{"missing separator", []string{"/data/agent-runs/x", "bash"}, false, false},
		{"relative run dir", []string{"agent-runs/x", "--", "bash"}, false, false},
		{"dot dot", []string{"/data/../etc", "--", "bash"}, false, false},
		{"no command", []string{"/data/agent-runs/x", "--"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, terminal, _, err := parseSandboxExec(tc.args)
			if (err == nil) != tc.ok {
				t.Fatalf("parseSandboxExec(%v) err = %v, want ok=%v", tc.args, err, tc.ok)
			}
			if terminal != tc.terminal {
				t.Fatalf("terminal = %v, want %v", terminal, tc.terminal)
			}
		})
	}
}

func TestTerminalEnvHasNoParentSecrets(t *testing.T) {
	t.Setenv("EGGY_TEST_SECRET", "s3cr3t")
	dir := filepath.Join(t.TempDir(), "terminal")
	env := terminalEnv(dir)
	for _, entry := range env {
		if strings.Contains(entry, "s3cr3t") || strings.HasPrefix(entry, "EGGY_") {
			t.Fatalf("terminal environment inherits %q", entry)
		}
	}
	for _, want := range []string{"TERM=xterm-256color", "HC_SOCKET=" + filepath.Join(dir, "home", ".hc.sock")} {
		if !slices.Contains(env, want) {
			t.Fatalf("terminal environment lacks %q: %v", want, env)
		}
	}
	if !slices.Contains(env, "PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin:"+filepath.Join(dir, "home", ".local", "bin")) {
		t.Fatalf("terminal PATH must end, not start, with ~/.local/bin, so an install cannot replace bash or hc: %v", env)
	}
}

// The command is found on the sandbox's PATH, never the parent's.
func TestLookPathUsesSandboxPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, err := lookPath("tool", []string{"PATH=/nonexistent"}); err == nil {
		t.Fatal("found a command on the parent's PATH")
	}
	if got, err := lookPath("tool", []string{"PATH=/nonexistent:" + dir}); err != nil || got != bin {
		t.Fatalf("lookPath = %q, %v", got, err)
	}
}

// ExecMain must fail closed: a bad command line exits 125 and runs nothing.
func TestSandboxExecFailsClosed(t *testing.T) {
	if code := ExecMain([]string{"relative/dir", "--", "true"}); code != sandboxExecExitFailed {
		t.Fatalf("exit = %d, want %d", code, sandboxExecExitFailed)
	}
}

func TestPrepareRunDirMakesWritableSubdirs(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	if err := MakeRunDirs(run); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"ws", "home", "tmp"} {
		if info, err := os.Stat(filepath.Join(run, sub)); err != nil || !info.IsDir() {
			t.Fatalf("missing %s", sub)
		}
	}
}
