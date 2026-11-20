package agentrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Runs clone from the mirror, the mirror follows the remote, and a run's
// checkout shares no file with the mirror (it is handed to the account).
func TestRunsCloneFromAMirror(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	remote := t.TempDir()
	git(t, remote, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, remote, "add", ".")
	git(t, remote, "commit", "-qm", "one")

	data := t.TempDir()
	w := NewWorkspaces(filepath.Join(data, "agent-runs"), &fakeRunner{}).WithMirrors(filepath.Join(data, "mirrors"))
	if err := os.MkdirAll(filepath.Join(data, "agent-runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := w.Clone(context.Background(), "", runIDA, "repo", remote, "main", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "mirrors", "repo.git", "HEAD")); err != nil {
		t.Fatalf("no mirror: %v", err)
	}
	// No object in the checkout may be a hard link into the mirror.
	_ = filepath.WalkDir(filepath.Join(w.Dir(runIDA), "ws", ".git", "objects"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, _ := os.Stat(path); info.Sys().(*syscall.Stat_t).Nlink > 1 {
				t.Fatalf("%s is hard-linked", path)
			}
		}
		return nil
	})

	if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, remote, "commit", "-qam", "two")
	second := strings.Repeat("c", 32)
	head, err := w.Clone(context.Background(), "", second, "repo", remote, "main", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if head == first || head != git(t, remote, "rev-parse", "HEAD") {
		t.Fatalf("second run did not see the new commit: %s (first %s)", head, first)
	}
	if _, err := w.Mirror(context.Background(), "../escape", remote, "tok"); err == nil {
		t.Fatal("a mirror name with a path in it was accepted")
	}
}
