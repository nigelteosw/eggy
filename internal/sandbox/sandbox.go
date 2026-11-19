package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrUnsupported is returned on any platform other than Linux, where
// the Landlock-based confinement this extension relies on does not exist.
var ErrUnsupported = errors.New("sandbox unsupported on this platform")

// Limits the sandbox enforces on every command. They are constants, not
// config: a run's cost ceiling is part of what the owner agreed to.
const (
	defaultCommandTimeout = 5 * time.Minute
	// outputTailBytes is how much of a command's output the model reads: the
	// end, kept under the core's bound on one tool result so it is never cut
	// from its start. maxFullOutput bounds the copy kept in the run directory.
	outputTailBytes = 40 << 10
	maxFullOutput   = 8 << 20
	// sandboxExecExitFailed is what the sandbox-exec entry point returns when
	// it cannot confine itself. The probe treats it as a failure, never as a
	// denial that proves the sandbox works.
	sandboxExecExitFailed = 125
)

// Spec is one command to run inside a run's sandbox.
type Spec struct {
	RunDir    string        // <data>/agent-runs/<id>; its ws/, home/ and tmp/ are the only writable paths
	Argv      []string      // program and arguments; Argv[0] is resolved on the sandbox PATH
	Timeout   time.Duration // zero means defaultCommandTimeout
	OutputCap int           // bytes of the end of the output kept; zero means outputTailBytes
	// FullOutputDir, when set, receives a new file holding the command's whole
	// output, up to maxFullOutput. Its path comes back in the result.
	FullOutputDir string
}

// Result is what one command produced. Output is the end of the
// command's output; Truncated says whether anything before it was dropped.
type Result struct {
	ExitCode  int
	Output    string
	TimedOut  bool
	Truncated bool
	// FullOutputPath is the file holding the whole output, when one was asked
	// for. FullOutputIncomplete says it stopped at maxFullOutput or on a write
	// error.
	FullOutputPath       string
	FullOutputIncomplete bool
}

// runEnv is the complete environment a sandboxed command sees. It is built
// from nothing, never from the parent's environment, so no Eggy secret can
// reach a command by inheritance. Git config is run-local and global-only, so
// no credential helper or system config applies.
func runEnv(runDir string) []string {
	home := filepath.Join(runDir, "home")
	return []string{
		"PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin",
		"HOME=" + home,
		"TMPDIR=" + filepath.Join(runDir, "tmp"),
		"LANG=C.UTF-8",
		"GOMODCACHE=" + filepath.Join(home, "go", "pkg", "mod"),
		"GOCACHE=" + filepath.Join(home, ".cache", "go-build"),
		"npm_config_cache=" + filepath.Join(home, ".npm"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

// MakeRunDirs creates a run's three writable directories. Ownership is set
// by the platform code, which knows the coder account.
func MakeRunDirs(runDir string) error {
	for _, sub := range []string{"ws", "home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// newProbeID is a random suffix for the probe's scratch directory.
func newProbeID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// validRunDir refuses anything that is not an absolute path to a run's
// directory. Paths come from server-chosen ids, but the sandbox-exec entry
// point is a command line, so it checks again rather than trust its caller.
func validRunDir(runDir string) error {
	if !filepath.IsAbs(runDir) || strings.Contains(runDir, "..") {
		return fmt.Errorf("run directory %q must be an absolute path without ..", runDir)
	}
	return nil
}

// TerminalDir is the terminal extension's one sandbox directory under the data
// directory. LockDataDir keeps its ws/, home/ and tmp/ with coder.
const TerminalDir = "terminal"

// terminalFlag selects terminal mode on the sandbox-exec command line.
const terminalFlag = "--terminal"

// terminalEnv is runEnv plus what an interactive terminal needs: a terminal
// type, the owner's own installs in ~/.local/bin (where Claude Code's
// installer puts it), and herdr-core's session and socket under the home, so
// nothing depends on a login session or XDG_RUNTIME_DIR the container lacks.
// Like runEnv it is built from nothing.
func terminalEnv(dir string) []string {
	home := filepath.Join(dir, "home")
	env := runEnv(dir)
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			env[i] = "PATH=" + filepath.Join(home, ".local", "bin") + ":" + strings.TrimPrefix(entry, "PATH=")
		}
	}
	return append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"SHELL=/bin/bash",
		"HC_SESSION=eggy",
		"HC_SOCKET="+filepath.Join(home, ".hc.sock"),
	)
}

// envFor is the environment for a mode.
func envFor(dir string, terminal bool) []string {
	if terminal {
		return terminalEnv(dir)
	}
	return runEnv(dir)
}

// lookPath resolves name against the PATH in env, never the parent's: the
// command must be one the sandbox itself would find.
func lookPath(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		for _, dir := range filepath.SplitList(strings.TrimPrefix(entry, "PATH=")) {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%q not found on the sandbox PATH", name)
}

// parseSandboxExec splits "[--terminal] <runDir> -- <argv...>" as the
// sandbox-exec entry point receives it.
func parseSandboxExec(args []string) (runDir string, terminal bool, argv []string, err error) {
	if len(args) > 0 && args[0] == terminalFlag {
		terminal, args = true, args[1:]
	}
	if len(args) < 3 || args[1] != "--" {
		return "", false, nil, errors.New("usage: sandbox-exec [--terminal] <run-dir> -- <command> [args...]")
	}
	if err := validRunDir(args[0]); err != nil {
		return "", false, nil, err
	}
	return args[0], terminal, args[2:], nil
}

// Within reports whether path is dir or below it, comparing cleaned paths
// lexically. Callers resolve symlinks first when the distinction matters.
func Within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}
