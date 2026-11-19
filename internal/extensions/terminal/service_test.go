package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeCore is herdr-core as the wire shows it: a plain-mode hello, then
// framed JSON-RPC on channel 0, an event channel, and attach channels.
type fakeCore struct {
	t          *testing.T
	generation int
	in         io.Reader // what the client wrote
	out        io.Writer // what the client reads
	wmu        sync.Mutex

	mu       sync.Mutex
	calls    []rpcCall
	frames   []frame // non-RPC frames the client sent
	next     uint32
	events   uint32
	nextAddr int
}

type rpcCall struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type duplex struct {
	io.Reader
	io.Writer
	closed chan struct{}
	once   sync.Once
}

func (d *duplex) Close() error { d.once.Do(func() { close(d.closed) }); return nil }

// fakeLauncher hands each Connect a fresh fakeCore and records the directory.
type fakeLauncher struct {
	t          *testing.T
	generation int
	mu         sync.Mutex
	dirs       []string
	cores      []*fakeCore
}

func (l *fakeLauncher) Connect(dir string) (Conn, error) {
	clientR, coreW := io.Pipe()
	coreR, clientW := io.Pipe()
	gen := l.generation
	if gen == 0 {
		gen = protocolGeneration
	}
	core := &fakeCore{t: l.t, generation: gen, in: coreR, out: coreW, next: 10}
	l.mu.Lock()
	l.dirs = append(l.dirs, dir)
	l.cores = append(l.cores, core)
	l.mu.Unlock()
	go core.serve()
	return &duplex{Reader: clientR, Writer: clientW, closed: make(chan struct{})}, nil
}

func (l *fakeLauncher) last() *fakeCore {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cores[len(l.cores)-1]
}

func (f *fakeCore) send(fr frame) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_ = writeFrame(f.out, fr)
}

func (f *fakeCore) reply(id any, result any) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	f.send(frame{channel: 0, kind: kindData, payload: body})
}

// event pushes one notification on the event channel.
func (f *fakeCore) event(method string, params any) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	f.mu.Lock()
	ch := f.events
	f.mu.Unlock()
	f.send(frame{channel: ch, kind: kindData, payload: append(body, '\n')})
}

func (f *fakeCore) serve() {
	r := bufio.NewReader(f.in)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var hello struct {
		ID any `json:"id"`
	}
	_ = json.Unmarshal(line, &hello)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": hello.ID, "result": map[string]any{"generation": f.generation, "framed": true}})
	_, _ = f.out.Write(append(body, '\n'))
	for {
		fr, err := readFrame(r)
		if err != nil {
			return
		}
		if fr.channel != 0 {
			f.mu.Lock()
			f.frames = append(f.frames, fr)
			f.mu.Unlock()
			continue
		}
		var call struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(fr.payload, &call) != nil {
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, rpcCall{Method: call.Method, Params: call.Params})
		f.mu.Unlock()
		switch call.Method {
		case "events.subscribe":
			f.mu.Lock()
			f.next++
			f.events = f.next
			ch := f.events
			f.mu.Unlock()
			f.reply(call.ID, map[string]any{"seq": 0, "channel": ch, "terminals": []any{}})
		case "terminal.run":
			f.mu.Lock()
			f.nextAddr++
			addr := "t" + string(rune('0'+f.nextAddr))
			f.mu.Unlock()
			f.reply(call.ID, map[string]any{"address": addr, "state": "live", "labels": call.Params["labels"], "created_ms": 1})
		case "terminal.attach":
			f.mu.Lock()
			f.next++
			ch := f.next
			f.mu.Unlock()
			f.reply(call.ID, map[string]any{"channel": ch, "terminal": map[string]any{}})
			// The first paint follows the reply at once, as the real core does.
			f.send(frame{channel: ch, kind: kindData, payload: []byte("first paint")})
		default:
			f.reply(call.ID, map[string]any{})
		}
	}
}

func (f *fakeCore) callsNamed(method string) []rpcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpcCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeCore) framesOfKind(kind byte) []frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []frame
	for _, fr := range f.frames {
		if fr.kind == kind {
			out = append(out, fr)
		}
	}
	return out
}

func newTestService(t *testing.T, l *fakeLauncher) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	svc, err := New(Options{
		Root:     root,
		DirName:  func(account string) string { return "dir-" + account },
		Prepare:  func(dir string) error { return os.MkdirAll(filepath.Join(dir, "ws"), 0o755) },
		Launcher: l,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, root
}

func as(account string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: account})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := frame{channel: 7, kind: kindResize, payload: resizePayload(120, 40)}
	if err := writeFrame(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.channel != 7 || got.kind != kindResize || binary.LittleEndian.Uint16(got.payload[0:2]) != 120 || binary.LittleEndian.Uint16(got.payload[2:4]) != 40 {
		t.Fatalf("round trip = %+v", got)
	}
	if err := writeFrame(&buf, frame{payload: make([]byte, maxFrame+1)}); err == nil {
		t.Fatal("wrote a frame over the limit")
	}
	var header [frameHeader]byte
	binary.LittleEndian.PutUint32(header[5:9], maxFrame+1)
	if _, err := readFrame(bytes.NewReader(header[:])); err == nil {
		t.Fatal("read a frame over the limit")
	}
}

func TestHelloRefusesOtherGeneration(t *testing.T) {
	svc, _ := newTestService(t, &fakeLauncher{t: t, generation: 2})
	_, err := svc.Terminals(as("alice"))
	if err == nil || !strings.Contains(err.Error(), "generation 2") {
		t.Fatalf("err = %v, want a generation refusal", err)
	}
}

func TestNoPrincipalIsRefused(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, _ := newTestService(t, l)
	if _, err := svc.Terminals(context.Background()); err == nil {
		t.Fatal("listed terminals with no account on the context")
	}
	if len(l.dirs) != 0 {
		t.Fatal("started a core with no account")
	}
}

// Each account runs its own core in its own directory, so one account's
// terminals are not even addressable from another's.
func TestAccountsGetSeparateCores(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, root := newTestService(t, l)
	if _, err := svc.Terminals(as("alice")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Terminals(as("bob")); err != nil {
		t.Fatal(err)
	}
	if len(l.dirs) != 2 || l.dirs[0] != filepath.Join(root, "dir-alice") || l.dirs[1] != filepath.Join(root, "dir-bob") {
		t.Fatalf("dirs = %v", l.dirs)
	}
	// A second call reuses the connection.
	if _, err := svc.Terminals(as("alice")); err != nil {
		t.Fatal(err)
	}
	if len(l.dirs) != 2 {
		t.Fatalf("reconnected: %v", l.dirs)
	}
}

func TestStartRunsAFixedCommandInTheSpace(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, root := newTestService(t, l)
	ctx := as("alice")
	if _, err := svc.Start(ctx, "web", "shell", 80, 24); err != ErrNotFound {
		t.Fatalf("started in a space that does not exist: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dir-alice", "ws", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, "web", "rm -rf /", 80, 24); err == nil {
		t.Fatal("started a command that is not on the list")
	}
	term, err := svc.Start(ctx, "web", "claude", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if term.Address == "" || term.Space != "web" {
		t.Fatalf("terminal = %+v", term)
	}
	runs := l.last().callsNamed("terminal.run")
	if len(runs) != 1 {
		t.Fatalf("terminal.run calls = %d", len(runs))
	}
	p := runs[0].Params
	if p["cwd"] != filepath.Join(root, "dir-alice", "ws", "web") {
		t.Fatalf("cwd = %v", p["cwd"])
	}
	if labels, _ := p["labels"].(map[string]any); labels[spaceLabel] != "web" || labels[commandLabel] != "claude" {
		t.Fatalf("labels = %v", p["labels"])
	}
	if _, named := p["name"]; named {
		t.Fatal("terminal.run was given a name, which must be unique among live terminals")
	}
	if p["no_parent"] != true {
		t.Fatal("terminal would end with the connection that started it")
	}
	if _, err := svc.Start(ctx, "../etc", "shell", 80, 24); err != ErrNotFound {
		t.Fatalf("a path as a space: %v", err)
	}
}

func TestAttachDeliversFirstPaintAndForwardsInput(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, root := newTestService(t, l)
	ctx := as("alice")
	_ = os.MkdirAll(filepath.Join(root, "dir-alice", "ws", "web"), 0o755)
	term, err := svc.Start(ctx, "web", "shell", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Attach(ctx, "t9", 80, 24); err != ErrNotFound {
		t.Fatalf("attached to a terminal the account does not have: %v", err)
	}
	stream, err := svc.Attach(ctx, term.Address, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-stream.Output():
		if string(got) != "first paint" {
			t.Fatalf("first output = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first paint, sent right after the reply, was lost")
	}
	attach := l.last().callsNamed("terminal.attach")[0].Params
	if attach["history"] != float64(historyLines) || attach["cols"] != float64(120) {
		t.Fatalf("attach params = %v", attach)
	}
	if _, err := stream.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := stream.Consumed(11); err != nil {
		t.Fatal(err)
	}
	core := l.last()
	eventually(t, "input, resize and credit frames", func() bool {
		return len(core.framesOfKind(kindData)) == 1 && len(core.framesOfKind(kindResize)) == 1 && len(core.framesOfKind(kindCredit)) >= 1
	})
	if got := string(core.framesOfKind(kindData)[0].payload); got != "ls\r" {
		t.Fatalf("input = %q", got)
	}
	_ = stream.Close()
	eventually(t, "close frame", func() bool { return len(core.framesOfKind(kindClose)) == 1 })
}

func TestStatusEventsFillNeedsYou(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, root := newTestService(t, l)
	ctx := as("alice")
	_ = os.MkdirAll(filepath.Join(root, "dir-alice", "ws", "web"), 0o755)
	term, err := svc.Start(ctx, "web", "claude", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	core := l.last()
	core.event("event", map[string]any{"seq": 1, "type": "status", "terminal": term.Address, "data": map[string]any{"status": map[string]any{"state": "blocked", "app": "claude", "msg": "Allow edit?"}}})
	eventually(t, "blocked terminal in NeedsYou", func() bool {
		ts, _ := svc.NeedsYou(ctx)
		return len(ts) == 1 && ts[0].Address == term.Address && ts[0].Message == "Allow edit?"
	})
	// Another account's list is its own core's: empty.
	if ts, _ := svc.NeedsYou(as("bob")); len(ts) != 0 {
		t.Fatalf("bob sees %v", ts)
	}
	core.event("event", map[string]any{"seq": 2, "type": "finished", "terminal": term.Address, "data": map[string]any{"exit_code": 0}})
	eventually(t, "finished terminal leaves NeedsYou", func() bool {
		ts, _ := svc.NeedsYou(ctx)
		return len(ts) == 0
	})
	// A missed snapshot replaces what was known.
	core.event("missed", map[string]any{"seq": 5, "terminals": []any{}})
	eventually(t, "snapshot replaced", func() bool {
		ts, _ := svc.Terminals(ctx)
		return len(ts) == 0
	})
}

func TestSpacesListFoldersAndTheirTerminals(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, root := newTestService(t, l)
	ctx := as("alice")
	ws := filepath.Join(root, "dir-alice", "ws")
	for _, name := range []string{"web", "api", ".hidden", "Not Valid"} {
		_ = os.MkdirAll(filepath.Join(ws, name), 0o755)
	}
	if _, err := svc.Start(ctx, "web", "shell", 80, 24); err != nil {
		t.Fatal(err)
	}
	spaces, err := svc.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(spaces) != 2 || spaces[0].Name != "api" || spaces[1].Name != "web" || len(spaces[1].Terminals) != 1 {
		t.Fatalf("spaces = %+v", spaces)
	}
	if err := svc.CreateSpace(ctx, "../escape"); err == nil {
		t.Fatal("created a space outside the workspace")
	}
	if err := svc.CreateSpace(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	execs := l.last().callsNamed("machine.exec")
	last := execs[len(execs)-1].Params["argv"].([]any)
	if last[len(last)-1] != filepath.Join(ws, "docs") {
		t.Fatalf("mkdir argv = %v", last)
	}
}

func TestStatusHooksAreWrittenOnceAndNeverOverwrite(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, _ := newTestService(t, l)
	if _, err := svc.Terminals(as("alice")); err != nil {
		t.Fatal(err)
	}
	execs := l.last().callsNamed("machine.exec")
	if len(execs) != 1 {
		t.Fatalf("machine.exec calls = %d", len(execs))
	}
	script := execs[0].Params["argv"].([]any)[2].(string)
	if !strings.HasPrefix(script, `[ -e "$HOME/.claude/settings.json" ] ||`) {
		t.Fatalf("hook install would overwrite an existing file: %s", script)
	}
	raw, err := base64.StdEncoding.DecodeString(execs[0].Params["input"].(string))
	if err != nil {
		t.Fatalf("hook input is not base64: %v", err)
	}
	input := string(raw)
	for _, want := range []string{"Notification", "state=blocked:app=claude", "Stop", "state=done"} {
		if !strings.Contains(input, want) {
			t.Fatalf("hook settings lack %q: %s", want, input)
		}
	}
}

func TestKillRefusesUnknownTerminal(t *testing.T) {
	l := &fakeLauncher{t: t}
	svc, _ := newTestService(t, l)
	if err := svc.Kill(as("alice"), "t1"); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	if len(l.last().callsNamed("terminal.kill")) != 0 {
		t.Fatal("asked the core to kill a terminal the account does not have")
	}
}
