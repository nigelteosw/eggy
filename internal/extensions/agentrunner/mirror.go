package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// mirrorName is what a repository's name must look like to name its mirror:
// one path element, no dots at the start.
var mirrorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// mirrorLocks serializes fetches of one mirror, so two runs starting on the
// same repository do not fetch into it at once.
var mirrorLocks sync.Map

// WithMirrors keeps a bare mirror of each repository under root and clones
// runs from it. Only Mirror, as root, holds the token; nothing cloned from the
// mirror ever sees it. See the shared-workspace design.
func (w *Workspaces) WithMirrors(root string) *Workspaces {
	w.mirrors = root
	return w
}

// Mirror brings the repository's mirror up to date with cloneURL and returns
// its path: created by a bare clone the first time, then fetched. The URL is
// given on every fetch rather than read from the mirror's config, and hooks
// are disabled, so nothing stored in the mirror decides what root runs.
func (w *Workspaces) Mirror(ctx context.Context, name, cloneURL, token string) (string, error) {
	if !mirrorName.MatchString(name) {
		return "", fmt.Errorf("repository name %q cannot name a mirror", name)
	}
	lock, _ := mirrorLocks.LoadOrStore(name, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	if err := os.MkdirAll(w.mirrors, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(w.mirrors, name+".git")
	home, err := os.MkdirTemp(w.root, ".mirror-home-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(home)
	env, cleanup, err := gitEnv(w.root, home, token)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		partial := path + ".partial"
		_ = os.RemoveAll(partial)
		if err := gitRun(ctx, env, "", token, "clone", "--bare", "--no-tags", cloneURL, partial); err != nil {
			_ = os.RemoveAll(partial)
			return "", err
		}
		if err := gitRun(ctx, env, partial, token, "config", "core.hooksPath", "/dev/null"); err != nil {
			return "", err
		}
		if err := os.Rename(partial, path); err != nil {
			return "", err
		}
		return path, nil
	} else if err != nil {
		return "", err
	}
	if err := gitRun(ctx, env, path, token, "-c", "core.hooksPath=/dev/null", "fetch", "--prune", "--no-tags", cloneURL, "+refs/heads/*:refs/heads/*"); err != nil {
		return "", err
	}
	return path, nil
}
