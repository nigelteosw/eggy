package agentrunner

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
		name string
		args []string
		ok   bool
	}{
		{"valid", []string{"/data/agent-runs/x", "--", "bash", "-c", "ls"}, true},
		{"missing separator", []string{"/data/agent-runs/x", "bash"}, false},
		{"relative run dir", []string{"agent-runs/x", "--", "bash"}, false},
		{"dot dot", []string{"/data/../etc", "--", "bash"}, false},
		{"no command", []string{"/data/agent-runs/x", "--"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseSandboxExec(tc.args)
			if (err == nil) != tc.ok {
				t.Fatalf("parseSandboxExec(%v) err = %v, want ok=%v", tc.args, err, tc.ok)
			}
		})
	}
}

// SandboxExecMain must fail closed: a bad command line exits 125 and runs nothing.
func TestSandboxExecFailsClosed(t *testing.T) {
	if code := SandboxExecMain([]string{"relative/dir", "--", "true"}); code != sandboxExecExitFailed {
		t.Fatalf("exit = %d, want %d", code, sandboxExecExitFailed)
	}
}

func TestPrepareRunDirMakesWritableSubdirs(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	if err := makeRunDirs(run); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"ws", "home", "tmp"} {
		if info, err := os.Stat(filepath.Join(run, sub)); err != nil || !info.IsDir() {
			t.Fatalf("missing %s", sub)
		}
	}
}
