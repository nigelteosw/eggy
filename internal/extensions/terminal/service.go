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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
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
		return []string{"bash", "-l"}, nil
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
	Logger   *slog.Logger
	Now      func() time.Time
}

// Service is the one path to terminals. The panel is a thin caller of it.
type Service struct {
	opts  Options
	mu    sync.Mutex // guards slots only; never held while talking to a core
	slots map[string]*slot
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
	Name      string     `json:"name"`
	Terminals []Terminal `json:"terminals"`
}

// core is one account's connection to its hc core and what it has learned
// from the event stream.
type core struct {
	dir    string
	conn   Conn
	client *client
	mu     sync.Mutex
	terms  map[string]Terminal
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

// connect prepares dir, starts its core and greets it, all within
// connectTimeout. On timeout the connection is closed, which ends any read
// still waiting on it.
func (s *Service) connect(ctx context.Context, dir string) (*core, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := s.opts.Prepare(dir); err != nil {
		return nil, fmt.Errorf("terminal: prepare: %w", err)
	}
	conn, err := s.opts.Launcher.Connect(dir)
	if err != nil {
		return nil, fmt.Errorf("terminal: start herdr-core: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	reader := bufio.NewReader(conn)
	if err := hello(reader, conn); err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("terminal: herdr-core did not answer: %w", ctx.Err())
		}
		return nil, err
	}
	c := &core{dir: dir, conn: conn, terms: map[string]Terminal{}}
	c.client = newClient(reader, conn, c.onEvent)
	snapshot, err := c.client.subscribe(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	c.applySnapshot(snapshot)
	if err := installStatusHooks(ctx, c.client); err != nil {
		s.opts.Logger.Warn("terminal: Claude Code status hooks not installed", "error", err)
	}
	// Greeted in time: from here the connection lives as long as the core.
	if !stop() {
		_ = conn.Close()
		return nil, fmt.Errorf("terminal: herdr-core did not answer: %w", ctx.Err())
	}
	return c, nil
}

// hcTerminal is the part of herdr-core's Terminal record Eggy reads.
type hcTerminal struct {
	Address  string            `json:"address"`
	Name     *string           `json:"name"`
	Title    string            `json:"title"`
	State    string            `json:"state"`
	Labels   map[string]string `json:"labels"`
	ExitCode *int              `json:"exit_code"`
	Status   *hcStatus         `json:"status"`
	StatusMS *int64            `json:"status_ms"`
	Created  int64             `json:"created_ms"`
}

type hcStatus struct {
	State string `json:"state"`
	Msg   string `json:"msg"`
	App   string `json:"app"`
}

func toTerminal(t hcTerminal) Terminal {
	out := Terminal{
		Address: t.Address, Space: t.Labels[spaceLabel], Title: t.Title,
		Live: t.State == "live", ExitCode: t.ExitCode,
		Changed: time.UnixMilli(t.Created),
	}
	out.Name = t.Labels[commandLabel]
	if out.Name == "" && t.Name != nil {
		out.Name = *t.Name
	}
	if t.Status != nil {
		out.Status, out.Message, out.App = t.Status.State, t.Status.Msg, t.Status.App
	}
	if t.StatusMS != nil {
		out.Changed = time.UnixMilli(*t.StatusMS)
	}
	return out
}

func (c *core) applySnapshot(raw json.RawMessage) {
	var snap struct {
		Terminals []hcTerminal `json:"terminals"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.terms = map[string]Terminal{}
	for _, t := range snap.Terminals {
		// Terminals a program started with hc itself carry no space; they are
		// still the owner's and are listed under the space of their parent
		// folder's choosing, which is none.
		c.terms[t.Address] = toTerminal(t)
	}
}

// onEvent applies one notification, as herdr-core's schema/README.md says.
func (c *core) onEvent(n eventNotice) {
	switch n.Method {
	case "missed":
		c.applySnapshot(n.Params)
		return
	case "event":
	default:
		return
	}
	var ev struct {
		Type     string          `json:"type"`
		Terminal string          `json:"terminal"`
		Data     json.RawMessage `json:"data"`
	}
	if json.Unmarshal(n.Params, &ev) != nil || ev.Terminal == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.terms[ev.Terminal]
	switch ev.Type {
	case "created", "restarted":
		var rec hcTerminal
		if json.Unmarshal(ev.Data, &rec) == nil {
			c.terms[ev.Terminal] = toTerminal(rec)
		}
		return
	case "removed":
		delete(c.terms, ev.Terminal)
		return
	case "status":
		var d struct {
			Status *hcStatus `json:"status"`
		}
		if json.Unmarshal(ev.Data, &d) == nil {
			t.Status, t.Message, t.App = "", "", ""
			if d.Status != nil {
				t.Status, t.Message, t.App = d.Status.State, d.Status.Msg, d.Status.App
			}
			t.Changed = time.Now()
		}
	case "title":
		var d struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(ev.Data, &d) == nil {
			t.Title = d.Title
		}
	case "finished", "lost":
		var d struct {
			ExitCode *int `json:"exit_code"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		t.Live, t.ExitCode, t.Status = ev.Type == "lost", d.ExitCode, ""
		t.Changed = time.Now()
	case "labels":
		var d struct {
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal(ev.Data, &d) == nil {
			t.Space = d.Labels[spaceLabel]
		}
	default:
		return
	}
	if t.Address == "" {
		return // an event for a terminal the snapshot did not have
	}
	c.terms[ev.Terminal] = t
}

func (c *core) list() []Terminal {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Terminal, 0, len(c.terms))
	for _, t := range c.terms {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func (c *core) has(address string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.terms[address]
	return ok
}

// Terminals lists the calling account's terminals.
func (s *Service) Terminals(ctx context.Context) ([]Terminal, error) {
	c, err := s.coreFor(ctx)
	if err != nil {
		return nil, err
	}
	return c.list(), nil
}

// Spaces lists the account's spaces, each with its terminals. A terminal with
// no space, such as one a program started itself, is listed under "".
func (s *Service) Spaces(ctx context.Context) ([]Space, error) {
	c, err := s.coreFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(c.dir, "ws"))
	if err != nil {
		return nil, err
	}
	bySpace := map[string]*Space{}
	var order []string
	for _, e := range entries {
		if e.IsDir() && spaceName.MatchString(e.Name()) {
			bySpace[e.Name()] = &Space{Name: e.Name(), Terminals: []Terminal{}}
			order = append(order, e.Name())
		}
	}
	for _, t := range c.list() {
		sp := bySpace[t.Space]
		if sp == nil {
			sp = &Space{Name: t.Space, Terminals: []Terminal{}}
			bySpace[t.Space] = sp
			order = append(order, t.Space)
		}
		sp.Terminals = append(sp.Terminals, t)
	}
	sort.Strings(order)
	order = slices.Compact(order)
	out := make([]Space, 0, len(order))
	for _, name := range order {
		out = append(out, *bySpace[name])
	}
	return out, nil
}

// CreateSpace makes an empty folder in the account's workspace. The sandbox
// user owns it, because its programs work in it.
func (s *Service) CreateSpace(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if !spaceName.MatchString(name) {
		return fmt.Errorf("a space name is lowercase letters, digits, '.', '_' or '-', up to 48 characters")
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	// The folder is made by a program in the sandbox, as its owner, so the
	// extension never needs coder's ownership rights itself.
	return c.client.call(ctx, "machine.exec", map[string]any{
		"argv": []string{"mkdir", "-p", "--", filepath.Join(c.dir, "ws", name)},
	}, nil)
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
	cwd := filepath.Join(c.dir, "ws", space)
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return Terminal{}, ErrNotFound
	}
	params := map[string]any{
		"argv":   argv,
		"cwd":    cwd,
		"labels": map[string]string{spaceLabel: space, commandLabel: command},
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
