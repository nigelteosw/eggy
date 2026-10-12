package agentrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// commandRunner is the sandbox as the workspace sees it. The real Sandbox
// satisfies it; tests substitute a fake.
type commandRunner interface {
	Run(ctx context.Context, spec SandboxSpec) (SandboxResult, error)
	PrepareRunDir(runDir string) error
}

// runIDPattern is what a run id looks like: 16 random bytes, hex encoded. Dir
// refuses anything else, so a run id can never name a path outside the root.
var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Workspaces owns the run directories under the data volume.
type Workspaces struct {
	root   string
	runner commandRunner
}

// NewWorkspaces returns the workspace store rooted at root.
func NewWorkspaces(root string, runner commandRunner) *Workspaces {
	return &Workspaces{root: root, runner: runner}
}

// Dir is the run's directory. An id that is not 32 hex characters is a
// programming error, because ids come from the server, never from a user, so
// it panics rather than returning a path someone could have steered.
func (w *Workspaces) Dir(runID string) string {
	if !runIDPattern.MatchString(runID) {
		panic(fmt.Sprintf("agentrunner: run id %q is not 32 hex characters", runID))
	}
	return filepath.Join(w.root, runID)
}

// Clone creates the run's directory and checks out baseBranch of cloneURL into
// its ws/, then cuts the feature branch. It runs as root, outside the sandbox,
// because it is a trusted operation holding the PAT. The token travels only in
// the environment, through a GIT_ASKPASS helper that reads it there, so it
// never appears in argv, a URL or the repository's config.
func (w *Workspaces) Clone(ctx context.Context, runID, cloneURL, baseBranch, token string) (string, error) {
	dir := w.Dir(runID)
	if err := makeRunDirs(dir); err != nil {
		return "", err
	}
	env, cleanup, err := gitEnv(w.root, filepath.Join(dir, "home"), token)
	if err != nil {
		return "", err
	}
	defer cleanup()
	ws := filepath.Join(dir, "ws")
	if err := gitRun(ctx, env, "", token, "clone", "--no-tags", "--single-branch", "--branch", baseBranch, cloneURL, ws); err != nil {
		return "", err
	}
	base, err := gitOutput(ctx, env, ws, token, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := gitRun(ctx, env, ws, token, "checkout", "-b", "eggy/"+runID); err != nil {
		return "", err
	}
	// Hooks are a way to run code on commit; the agent's repository is not
	// allowed to supply any.
	if err := gitRun(ctx, env, ws, token, "config", "core.hooksPath", "/dev/null"); err != nil {
		return "", err
	}
	if err := w.runner.PrepareRunDir(dir); err != nil {
		return "", err
	}
	return base, nil
}

// gitEnv is the environment for a git process that needs the PAT. The token
// reaches git only through the environment, read by an askpass helper, so it
// is never in argv or a URL. The global config is disabled, so no credential
// helper or alias from the machine applies. The caller runs cleanup afterwards.
func gitEnv(root, home, token string) ([]string, func(), error) {
	dir, err := os.MkdirTemp(root, ".askpass-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	askpass := filepath.Join(dir, "askpass")
	script := "#!/bin/sh\ncase \"$1\" in\n  *sername*) echo x-access-token ;;\n  *) echo \"$EGGY_GIT_TOKEN\" ;;\nesac\n"
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"GIT_ASKPASS=" + askpass,
		"EGGY_GIT_TOKEN=" + token,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	}, cleanup, nil
}

// gitRun runs git and returns an error that never contains token.
func gitRun(ctx context.Context, env []string, dir, token string, args ...string) error {
	_, err := gitOutput(ctx, env, dir, token, args...)
	return err
}

func gitOutput(ctx context.Context, env []string, dir, token string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	cmd.Dir = dir
	// stdout and stderr stay apart: a warning on stderr must never end up in
	// a value like the base SHA that the caller reads from stdout.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Some git commands explain a refusal on stdout ("nothing to commit"),
		// so both streams are kept in the error, with the token removed.
		detail := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
		msg := strings.ReplaceAll(detail, token, "[redacted]")
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Remove deletes a run's directory.
func (w *Workspaces) Remove(runID string) error {
	return os.RemoveAll(w.Dir(runID))
}

// Sweep deletes every run directory keep does not retain. keep reports whether
// a run is retained and until when; a run with retain=false is removed at
// once, which is how a published run's workspace goes.
func (w *Workspaces) Sweep(now time.Time, keep func(runID string) (time.Time, bool)) error {
	entries, err := os.ReadDir(w.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		if until, retain := keep(entry.Name()); retain && now.Before(until) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(w.root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Usage is the total size of every run directory, so admission can refuse a
// run that would exceed the disk budget.
func (w *Workspaces) Usage() (int64, error) {
	var total int64
	err := filepath.WalkDir(w.root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// resolveInside maps a path the model supplied onto the workspace. Absolute
// paths and ".." are refused outright. Symlinks are resolved before the
// containment check, so a link inside ws/ that points outside it is refused too.
func resolveInside(ws, rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the workspace", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q escapes the workspace", rel)
	}
	joined := filepath.Join(ws, clean)
	real, err := resolveExisting(joined)
	if err != nil {
		return "", err
	}
	wsReal, err := filepath.EvalSymlinks(ws)
	if err != nil {
		return "", err
	}
	if !within(real, wsReal) {
		return "", fmt.Errorf("path %q resolves outside the workspace", rel)
	}
	return joined, nil
}

// resolveExisting resolves symlinks in the longest existing prefix of p, so a
// file that does not exist yet can still be checked against its parent.
func resolveExisting(p string) (string, error) {
	rest := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor of %q", p)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
