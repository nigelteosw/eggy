package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// newRepoService is a service with one configured repository, "app", whose
// mirror is a real bare repository with one commit.
func newRepoService(t *testing.T) (*Service, string, *fakeLauncher) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	src := t.TempDir()
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run(src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(src, "add", ".")
	run(src, "commit", "-qm", "one")
	mirror := filepath.Join(t.TempDir(), "app.git")
	run(filepath.Dir(mirror), "clone", "-q", "--bare", src, mirror)

	l := &fakeLauncher{t: t}
	root := t.TempDir()
	svc, err := New(Options{
		Root:     root,
		DirName:  func(a string) string { return "dir-" + a },
		Prepare:  func(dir string) error { return os.MkdirAll(filepath.Join(dir, "ws"), 0o755) },
		Launcher: l,
		Repos:    func() []string { return []string{"app"} },
		Mirror:   func(context.Context, string) (string, error) { return mirror, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, root, l
}

func spaceNamed(spaces []Space, name string) (Space, bool) {
	for _, s := range spaces {
		if s.Name == name {
			return s, true
		}
	}
	return Space{}, false
}

// A configured repository becomes a space with a working copy, without the
// owner doing anything, and stays a repository space.
func TestRepositoryBecomesASpace(t *testing.T) {
	svc, root, _ := newRepoService(t)
	ctx := as("alice")
	if _, err := svc.Spaces(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "working copy", func() bool {
		_, err := os.Stat(filepath.Join(root, "dir-alice", "ws", "app", "README"))
		return err == nil
	})
	spaces, err := svc.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app, ok := spaceNamed(spaces, "app")
	if !ok || !app.Repo || app.Preparing {
		t.Fatalf("app space: %+v %v", app, ok)
	}
	if err := svc.UpdateRepo(ctx, "app"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := svc.UpdateRepo(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("update of a non-repository: %v", err)
	}
}

// Spaces are created and deleted as folders, and the list follows; a
// repository's name is not a free space name, and its space cannot go.
func TestSpaceCreateAndDeleteStayInSync(t *testing.T) {
	svc, root, l := newRepoService(t)
	ctx := as("alice")
	if err := svc.CreateSpace(ctx, "notes"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateSpace(ctx, "app"); err == nil {
		t.Fatal("a space took a repository's name")
	}
	term, err := svc.Start(ctx, "notes", "shell", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSpace(ctx, "app"); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("deleted a repository space: %v", err)
	}
	if err := svc.DeleteSpace(ctx, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir-alice", "ws", "notes")); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	killed := false
	for _, c := range l.last().callsNamed("terminal.kill") {
		if c.Params["terminal"] == term.Address {
			killed = true
		}
	}
	if !killed {
		t.Fatal("the space's terminal was not ended")
	}
	spaces, _ := svc.Spaces(ctx)
	if _, ok := spaceNamed(spaces, "notes"); ok {
		t.Fatal("deleted space still listed")
	}
	if err := svc.DeleteSpace(ctx, "notes"); err != ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

// A space made with a repository's name before it was one is an empty
// folder; the working copy is cloned into it rather than the folder being
// taken for a copy.
func TestEmptyFolderWithARepositoryNameIsFilled(t *testing.T) {
	svc, root, _ := newRepoService(t)
	if err := os.MkdirAll(filepath.Join(root, "dir-alice", "ws", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Spaces(as("alice")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "working copy in the empty folder", func() bool {
		_, err := os.Stat(filepath.Join(root, "dir-alice", "ws", "app", "README"))
		return err == nil
	})
}

// A clone that fails says why on its space and is not retried on every
// listing; Fetch from GitHub retries it.
func TestFailedCopyIsShownAndRetriedOnUpdate(t *testing.T) {
	svc, root, _ := newRepoService(t)
	good := svc.opts.Mirror
	var calls atomic.Int32
	svc.opts.Mirror = func(ctx context.Context, name string) (string, error) {
		calls.Add(1)
		return "", errors.New("no token")
	}
	ctx := as("alice")
	var app Space
	eventually(t, "the failure shown", func() bool {
		spaces, _ := svc.Spaces(ctx)
		app, _ = spaceNamed(spaces, "app")
		return app.Error != ""
	})
	if !strings.Contains(app.Error, "no token") || app.Preparing {
		t.Fatalf("space: %+v", app)
	}
	before := calls.Load()
	_, _ = svc.Spaces(ctx)
	_, _ = svc.Spaces(ctx)
	if calls.Load() != before {
		t.Fatal("a failed copy is retried on every listing")
	}
	svc.opts.Mirror = good
	if err := svc.UpdateRepo(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir-alice", "ws", "app", "README")); err != nil {
		t.Fatal(err)
	}
	spaces, _ := svc.Spaces(ctx)
	if app, _ = spaceNamed(spaces, "app"); app.Error != "" {
		t.Fatalf("error kept after retry: %+v", app)
	}
}
