package agentrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner stands in for the sandbox: it records each command and answers
// with a fixed result, so tests of the tools need no Linux.
type fakeRunner struct {
	specs  []SandboxSpec
	result SandboxResult
}

func (f *fakeRunner) Run(_ context.Context, spec SandboxSpec) (SandboxResult, error) {
	f.specs = append(f.specs, spec)
	return f.result, nil
}

func (f *fakeRunner) PrepareRunDir(runDir string) error { return makeRunDirs(runDir) }

const runIDA = "0123456789abcdef0123456789abcdef"

func TestResolveInsideRejectsEscapes(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(ws, "l")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../x", "/etc/passwd", "l/passwd"} {
		if _, err := resolveInside(ws, rel); err == nil {
			t.Fatalf("resolveInside(%q) accepted an escape", rel)
		}
	}
	if _, err := resolveInside(ws, "a/b.go"); err != nil {
		t.Fatalf("resolveInside(a/b.go) refused: %v", err)
	}
}

func TestDirRejectsNonHexRunID(t *testing.T) {
	w := NewWorkspaces(t.TempDir(), &fakeRunner{})
	defer func() {
		if recover() == nil {
			t.Fatal("Dir accepted a run id that is not 32 hex characters")
		}
	}()
	w.Dir("../escape")
}

func TestTwoRunsSeparateDirs(t *testing.T) {
	w := NewWorkspaces(t.TempDir(), &fakeRunner{})
	other := strings.Repeat("b", 32)
	if w.Dir(runIDA) == w.Dir(other) {
		t.Fatal("two runs share a directory")
	}
}

// TestCloneNeverPutsTokenInArgvOrConfig uses a fake git that records its argv
// and environment, so the test can see exactly what a real clone would receive.
func TestCloneNeverPutsTokenInArgvOrConfig(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	// The clone environment is a whitelist, so the fake reaches its log by a
	// path written into the script rather than through an inherited variable.
	script := fmt.Sprintf(`#!/bin/sh
echo "ARGV $*" >> %[1]q
env | grep -E '^EGGY_GIT_TOKEN=' >> %[1]q
case "$1" in
  clone) for last; do :; done; mkdir -p "$last/.git"; printf '[core]\n' > "$last/.git/config" ;;
  rev-parse) echo deadbeef ;;
esac
exit 0
`, log)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	const token = "ghp_TOKEN_MUST_NOT_LEAK"
	root := t.TempDir()
	w := NewWorkspaces(root, &fakeRunner{})
	base, err := w.Clone(context.Background(), runIDA, "https://github.com/o/r.git", "main", token)
	if err != nil {
		t.Fatal(err)
	}
	if base != "deadbeef" {
		t.Fatalf("base sha = %q", base)
	}
	raw, _ := os.ReadFile(log)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ARGV") && strings.Contains(line, token) {
			t.Fatalf("token in argv: %s", line)
		}
	}
	if !strings.Contains(string(raw), "EGGY_GIT_TOKEN="+token) {
		t.Fatal("token was not passed through the environment")
	}
	cfg, _ := os.ReadFile(filepath.Join(w.Dir(runIDA), "ws", ".git", "config"))
	if strings.Contains(string(cfg), token) {
		t.Fatalf("token written to git config: %s", cfg)
	}
}

func TestSweepHonorsRetention(t *testing.T) {
	root := t.TempDir()
	w := NewWorkspaces(root, &fakeRunner{})
	kept := strings.Repeat("c", 32)
	gone := strings.Repeat("d", 32)
	for _, id := range []string{kept, gone} {
		if err := makeRunDirs(w.Dir(id)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	err := w.Sweep(now, func(id string) (time.Time, bool) {
		if id == kept {
			return now.Add(time.Hour), true
		}
		return time.Time{}, false
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Dir(kept)); err != nil {
		t.Fatal("retained run was removed")
	}
	if _, err := os.Stat(w.Dir(gone)); !os.IsNotExist(err) {
		t.Fatal("expired run was kept")
	}
}
