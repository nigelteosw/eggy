// Package terminal is the terminal extension: interactive terminals in the web
// panel, run by herdr-core (hc) inside the agent runner's sandbox, so an owner
// can install and use Claude Code inside Eggy and check on it from a phone.
//
// Each account gets its own sandbox directory, its own uid and its own hc
// core, so ordinary ownership keeps one account's programs out of another's
// files, socket and ptys, and Landlock confines each core to its directory. The extension holds no credential:
// whatever the owner signs in to inside a terminal stays in that directory.
//
// The design is docs/superpowers/specs/2026-11-19-terminal-design.md.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// spaceLabel is the label a terminal carries naming the space it runs in.
const spaceLabel = "eggy.space"

// commandLabel names which of Commands a terminal was started with. It is a
// label, not hc's name, because a name must be unique among live terminals
// and an owner may run two shells.
const commandLabel = "eggy.command"

// Commands is the fixed list a terminal may start with. The browser names one;
// it never sends a command line.
var Commands = []string{"shell", "claude", "codex"}

// claudeInstall is shown when Claude Code is missing, rather than failing
// with "command not found".
const claudeInstall = "curl -fsSL https://claude.ai/install.sh | bash"

func argvFor(command string) ([]string, error) {
	switch command {
	case "shell":
		// The owner's shell is zsh once they have a ~/.zshrc, bash otherwise.
		// Either is a login shell, so /etc/profile.d/eggy.sh sets its PATH.
		return []string{"bash", "-lc", `[ -f "$HOME/.zshrc" ] && command -v zsh >/dev/null && exec zsh -l; exec bash -l`}, nil
	case "claude":
		return []string{"bash", "-lc", `command -v claude >/dev/null || { printf 'Claude Code is not installed here yet. Install it with:\n\n  ` + claudeInstall + `\n\nthen run: claude\n\n'; exec bash -l; }; exec claude`}, nil
	case "codex":
		return []string{"bash", "-lc", `command -v codex >/dev/null || { printf 'Codex is not installed here yet. Install it with:\n\n  npm install -g --prefix ~/.local @openai/codex\n\nthen run: codex\n\n'; exec bash -l; }; exec codex`}, nil
	}
	return nil, fmt.Errorf("unknown command %q", command)
}

var spaceName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)

// ErrNotFound is a terminal or space the calling account does not have.
var ErrNotFound = errors.New("not found")

// Conn is one connection to an account's core: `hc connect`'s stdout and
// stdin. Close ends the connection, never the core.
type Conn interface {
	io.Reader
	io.Writer
	Close() error
}

// Launcher connects to the core that serves dir, starting it if needed.
type Launcher interface {
	Connect(dir string) (Conn, error)
}

// Options builds a Service.
type Options struct {
	// Root is <data>/terminal.
	Root string
	// DirName maps an account to its directory name under Root. It must be
	// unguessable from the account id (bootstrap keys it with the encryption
	// key) and stable across restarts.
	DirName func(accountID string) string
	// Prepare creates a directory's ws/, home/ and tmp/ for the account's own
	// sandbox user (sandbox.PrepareTerminalDir).
	Prepare  func(dir string) error
	Launcher Launcher
	// Repos names the configured repositories, each of which is a space in
	// every account's workspace. Mirror brings one's read-only mirror up
	// to date and returns its path; it is the agent runner's, as root, with
	// the token. Both nil means no repository spaces.
	Repos  func() []string
	Mirror func(ctx context.Context, name string) (string, error)
	// Runs lists the calling account's agent runs. Nil means none are shown.
	Runs   func(ctx context.Context) ([]Run, error)
	Logger *slog.Logger
	Now    func() time.Time
}

// Service is the one path to terminals. The panel is a thin caller of it.
type Service struct {
	opts   Options
	mu     sync.Mutex // guards slots only; never held while talking to a core
	slots  map[string]*slot
	copies repoState
}

// slot is one account's place in the service. Its lock is held while that
// account's core is started, so one slow or hung core holds up only its own
// account.
type slot struct {
	mu   sync.Mutex
	core *core
}

// connectTimeout bounds starting and greeting a core. A core that never
// answers is abandoned, not waited on.
const connectTimeout = 20 * time.Second

// New builds the service. Nothing starts until an account uses it.
func New(opts Options) (*Service, error) {
	if opts.Root == "" || opts.DirName == nil || opts.Prepare == nil || opts.Launcher == nil {
		return nil, errors.New("terminal: incomplete options")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, slots: map[string]*slot{}}, nil
}

// Terminal is one terminal as its owner sees it.
type Terminal struct {
	Address  string    `json:"address"`
	Space    string    `json:"space"`
	Run      string    `json:"run,omitempty"`
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Live     bool      `json:"live"`
	Status   string    `json:"status,omitempty"`
	Message  string    `json:"message,omitempty"`
	App      string    `json:"app,omitempty"`
	Changed  time.Time `json:"changed"`
	ExitCode *int      `json:"exit_code,omitempty"`
}

// Space is a folder in the account's workspace and the terminals in it.
type Space struct {
	Name string `json:"name"`
	// Repo marks a configured repository's working copy: made by Eggy from
	// the mirror, updated from it, and not deletable while configured.
	Repo bool `json:"repo,omitempty"`
	// Preparing means a repository space whose working copy is still being
	// made; it has no folder yet.
	Preparing bool `json:"preparing,omitempty"`
	// Error is why the working copy could not be made; Update retries.
	Error     string     `json:"error,omitempty"`
	Terminals []Terminal `json:"terminals"`
	// Runs are the agent runs on this repository with a checkout here.
	Runs []Run `json:"runs,omitempty"`
}

func principal(ctx context.Context) (string, error) {
	p, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	if p.AccountID == "" {
		return "", errors.New("terminal: no account")
	}
	return p.AccountID, nil
}

func (s *Service) dir(account string) string {
	return filepath.Join(s.opts.Root, s.opts.DirName(account))
}

// coreFor returns the account's live connection, connecting (and so starting
// the core) when there is none or the last one ended.
func (s *Service) coreFor(ctx context.Context) (*core, error) {
	account, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	sl := s.slots[account]
	if sl == nil {
		sl = &slot{}
		s.slots[account] = sl
	}
	s.mu.Unlock()

	sl.mu.Lock()
	defer sl.mu.Unlock()
	if c := sl.core; c != nil {
		select {
		case <-c.client.done:
			_ = c.conn.Close()
			sl.core = nil
		default:
			return c, nil
		}
	}
	c, err := s.connect(ctx, s.dir(account))
	if err != nil {
		return nil, err
	}
	sl.core = c
	return c, nil
}

// Terminals lists the calling account's terminals.
func (s *Service) Terminals(ctx context.Context) ([]Terminal, error) {
	c, err := s.coreFor(ctx)
	if err != nil {
		return nil, err
	}
	return c.list(), nil
}

// Start runs command in space and returns the new terminal.
func (s *Service) Start(ctx context.Context, space, command string, cols, rows uint16) (Terminal, error) {
	argv, err := argvFor(command)
	if err != nil {
		return Terminal{}, err
	}
	if !spaceName.MatchString(space) {
		return Terminal{}, ErrNotFound
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return Terminal{}, err
	}
	labels := map[string]string{spaceLabel: space, commandLabel: command}
	return s.start(ctx, c, argv, filepath.Join(c.dir, "ws", space), labels, cols, rows)
}

// start runs argv in cwd, which must be a folder, as a terminal carrying
// labels. Every way of starting a terminal ends here.
func (s *Service) start(ctx context.Context, c *core, argv []string, cwd string, labels map[string]string, cols, rows uint16) (Terminal, error) {
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return Terminal{}, ErrNotFound
	}
	params := map[string]any{
		"argv":   argv,
		"cwd":    cwd,
		"labels": labels,
		// A terminal outlives every viewer and every Eggy restart; it ends
		// when its program does or its owner kills it.
		"no_parent": true,
	}
	if cols > 0 && rows > 0 {
		params["size"] = map[string]any{"cols": cols, "rows": rows}
	}
	var rec hcTerminal
	if err := c.client.call(ctx, "terminal.run", params, &rec); err != nil {
		return Terminal{}, err
	}
	t := toTerminal(rec)
	c.mu.Lock()
	c.terms[t.Address] = t
	c.mu.Unlock()
	return t, nil
}

// Kill ends a terminal's program and removes it.
func (s *Service) Kill(ctx context.Context, address string) error {
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	if !c.has(address) {
		return ErrNotFound
	}
	if err := c.client.call(ctx, "terminal.kill", map[string]any{"terminal": address}, nil); err != nil {
		var re *rpcError
		if !errors.As(err, &re) || re.Data.Kind != "not_found" {
			return err
		}
	}
	_ = c.client.call(ctx, "terminal.rm", map[string]any{"terminal": address}, nil)
	c.mu.Lock()
	delete(c.terms, address)
	c.mu.Unlock()
	return nil
}

// Stream is an attached terminal.
type Stream interface {
	Output() <-chan []byte
	Reason() string
	Consumed(n int) error
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// historyLines is how much scrollback a viewer gets on attach.
const historyLines = 2000

// Attach connects a viewer to a terminal: the current screen and history
// first, then live output.
func (s *Service) Attach(ctx context.Context, address string, cols, rows uint16) (Stream, error) {
	c, err := s.coreFor(ctx)
	if err != nil {
		return nil, err
	}
	if !c.has(address) {
		return nil, ErrNotFound
	}
	if cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}
	var result struct {
		Channel uint32 `json:"channel"`
	}
	if err := c.client.call(ctx, "terminal.attach", map[string]any{
		"terminal": address, "cols": cols, "rows": rows, "history": historyLines,
	}, &result); err != nil {
		return nil, err
	}
	return c.client.openStream(result.Channel, false), nil
}

// NeedsYou lists the account's terminals whose program said it is blocked on
// its owner.
func (s *Service) NeedsYou(ctx context.Context) ([]Terminal, error) {
	ts, err := s.Terminals(ctx)
	if err != nil {
		return nil, err
	}
	out := []Terminal{}
	for _, t := range ts {
		if t.Live && t.Status == "blocked" {
			out = append(out, t)
		}
	}
	return out, nil
}

// Close ends every connection. The cores and their terminals keep running, so
// a restart of Eggy leaves the owner's programs where they were.
func (s *Service) Close() error {
	s.mu.Lock()
	slots := s.slots
	s.slots = map[string]*slot{}
	s.mu.Unlock()
	for _, sl := range slots {
		sl.mu.Lock()
		if sl.core != nil {
			_ = sl.core.conn.Close()
			sl.core = nil
		}
		sl.mu.Unlock()
	}
	return nil
}
