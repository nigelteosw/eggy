// Package home defines Eggy's on-disk home directory: the one place every
// durable artifact lives, with one function per artifact so no other package
// has to know a path literal.
//
//	<home>/
//	  config.yaml   startup settings
//	  .env          API keys and secrets, never read through the web API
//	  auth.json     OAuth provider credentials for MCP servers
//	  SOUL.md       durable agent identity, first slot in the system prompt
//	  memories/     MEMORY.md, USER.md, WATCH.md
//	  skills/       reviewed procedural skills
//	  cron/         scheduled jobs, one file per job
//	  logs/         gateway.log, errors.log (secrets redacted)
//	  state.json    internal runtime state
//	  eggy.db       conversation memory
//	  runs/         read-only repository checkouts
//
// Owner-facing Markdown is edited outside Eggy. Machine-managed files are not
// exposed through the web API.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRoot is where a container deployment keeps its persistent volume.
const DefaultRoot = "/data"

// Layout resolves every artifact path under one home root.
type Layout struct{ Root string }

// At returns the layout rooted at dir, expanding a leading ~ so EGGY_HOME
// can be written the way an operator would type it.
func At(dir string) Layout {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = DefaultRoot
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
		}
	}
	return Layout{Root: filepath.Clean(dir)}
}

// Log file names inside <home>/logs.
const (
	GatewayLogName = "gateway.log"
	ErrorsLogName  = "errors.log"
)

func (l Layout) Config() string   { return filepath.Join(l.Root, "config.yaml") }
func (l Layout) Env() string      { return filepath.Join(l.Root, ".env") }
func (l Layout) Auth() string     { return filepath.Join(l.Root, "auth.json") }
func (l Layout) Soul() string     { return filepath.Join(l.Root, "SOUL.md") }
func (l Layout) Memories() string { return filepath.Join(l.Root, "memories") }
func (l Layout) Memory() string   { return filepath.Join(l.Memories(), "MEMORY.md") }
func (l Layout) User() string     { return filepath.Join(l.Memories(), "USER.md") }
func (l Layout) Watch() string    { return filepath.Join(l.Memories(), "WATCH.md") }
func (l Layout) Skills() string   { return filepath.Join(l.Root, "skills") }
func (l Layout) Cron() string     { return filepath.Join(l.Root, "cron") }
func (l Layout) Logs() string     { return filepath.Join(l.Root, "logs") }
func (l Layout) State() string    { return filepath.Join(l.Root, "state.json") }
func (l Layout) Database() string { return filepath.Join(l.Root, "eggy.db") }
func (l Layout) Runs() string     { return filepath.Join(l.Root, "runs") }

// Directories lists every directory the layout owns, in creation order.
func (l Layout) Directories() []string {
	return []string{l.Root, l.Memories(), l.Skills(), l.Cron(), l.Logs()}
}

// Ensure creates the home directory and its subdirectories.
//
// Every subdirectory Eggy owns is forced to 0700, even one that already
// existed with looser bits, because the home holds `.env`, `auth.json`, and
// cloned repositories. The root itself is only created at 0700 and never
// chmodded: it is frequently a volume mount whose mode belongs to whoever
// provisioned it, and the files inside are written 0600 regardless.
func (l Layout) Ensure() error {
	directories := l.Directories()
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	for _, dir := range directories[1:] {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure %s: %w", dir, err)
		}
	}
	return nil
}

// Migrate moves artifacts left by Eggy's former flat home into their current
// places. It is idempotent, it never overwrites a file already at the target,
// and a home that is already current is left untouched.
func (l Layout) Migrate() error {
	if err := l.Ensure(); err != nil {
		return err
	}
	for _, move := range []struct{ from, to string }{
		{filepath.Join(l.Root, "MEMORY.md"), l.Memory()},
		{filepath.Join(l.Root, "USER.md"), l.User()},
	} {
		if err := relocate(move.from, move.to); err != nil {
			return err
		}
	}
	return nil
}

// relocate renames from onto to when to is absent. A target that already
// exists wins: it is the current file, and from is a leftover.
func relocate(from, to string) error {
	if _, err := os.Stat(from); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", from, err)
	}
	if _, err := os.Stat(to); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", to, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("move %s to %s: %w", from, to, err)
	}
	return nil
}

// Resolve picks the home root for a process. An explicit --home flag or
// EGGY_HOME wins; otherwise an EGGY_CONFIG pointing at a config file implies
// the home that contains it, which keeps existing deployments working; and
// with neither set the container default applies.
func Resolve(flagHome string, getenv func(string) string) Layout {
	if dir := strings.TrimSpace(flagHome); dir != "" {
		return At(dir)
	}
	if dir := strings.TrimSpace(getenv("EGGY_HOME")); dir != "" {
		return At(dir)
	}
	if configPath := strings.TrimSpace(getenv("EGGY_CONFIG")); configPath != "" {
		return At(filepath.Dir(configPath))
	}
	return At(DefaultRoot)
}
