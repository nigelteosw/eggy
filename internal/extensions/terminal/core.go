package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// core is one account's connection to its hc core and what it has learned
// from the event stream.
type core struct {
	dir    string
	conn   Conn
	client *client
	mu     sync.Mutex
	terms  map[string]Terminal
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
	// Repository spaces are made in the background: a clone can take longer
	// than any request should wait.
	s.ensureRepos(context.WithoutCancel(ctx), c)
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
