package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeTerminal struct {
	mu       sync.Mutex
	accounts []string
	started  []string
	stream   *fakeTerminalStream
}

func (f *fakeTerminal) seen(ctx context.Context) {
	p, _ := ports.PrincipalFromContext(ctx)
	f.mu.Lock()
	f.accounts = append(f.accounts, p.AccountID)
	f.mu.Unlock()
}

func (f *fakeTerminal) Commands() []string { return []string{"shell", "claude"} }
func (f *fakeTerminal) Spaces(ctx context.Context) ([]TerminalSpace, error) {
	f.seen(ctx)
	return []TerminalSpace{{Name: "web", Terminals: []TerminalInfo{{Address: "t1", Space: "web", Live: true}}}}, nil
}
func (f *fakeTerminal) CreateSpace(ctx context.Context, name string) error { f.seen(ctx); return nil }
func (f *fakeTerminal) Start(ctx context.Context, space, command string, cols, rows uint16) (TerminalInfo, error) {
	f.seen(ctx)
	f.mu.Lock()
	f.started = append(f.started, space+"/"+command)
	f.mu.Unlock()
	return TerminalInfo{Address: "t2", Space: space, Name: command, Live: true}, nil
}
func (f *fakeTerminal) Kill(ctx context.Context, address string) error {
	f.seen(ctx)
	if address != "t1" {
		return ErrTerminalNotFound
	}
	return nil
}
func (f *fakeTerminal) Attach(ctx context.Context, address string, cols, rows uint16) (TerminalStream, error) {
	f.seen(ctx)
	if address != "t1" {
		return nil, ErrTerminalNotFound
	}
	return f.stream, nil
}
func (f *fakeTerminal) NeedsYou(ctx context.Context) ([]TerminalInfo, error) {
	f.seen(ctx)
	return []TerminalInfo{{Address: "t1", Status: "blocked"}}, nil
}

type fakeTerminalStream struct {
	out     chan []byte
	mu      sync.Mutex
	input   []string
	resizes [][2]uint16
	closed  bool
}

func (s *fakeTerminalStream) Output() <-chan []byte { return s.out }
func (s *fakeTerminalStream) Reason() string        { return `{"reason":"exited","exit_code":0,"secret":"x"}` }
func (s *fakeTerminalStream) Consumed(int) error    { return nil }
func (s *fakeTerminalStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.input = append(s.input, string(p))
	s.mu.Unlock()
	return len(p), nil
}
func (s *fakeTerminalStream) Resize(c, r uint16) error {
	s.mu.Lock()
	s.resizes = append(s.resizes, [2]uint16{c, r})
	s.mu.Unlock()
	return nil
}
func (s *fakeTerminalStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func terminalHandler(t *testing.T, svc TerminalService) (http.Handler, *http.Cookie) {
	t.Helper()
	webConfig := testWebConfig(t, time.Now())
	if svc != nil {
		webConfig.Terminal = svc
	}
	handler := NewWebHandler("", webConfig)
	return handler, webLoginCookie(t, handler)
}

func TestSessionReportsTerminalOnlyWhenWired(t *testing.T) {
	for _, c := range []struct {
		name string
		svc  TerminalService
		want string
	}{{"disabled", nil, ""}, {"enabled", &fakeTerminal{}, "terminal"}} {
		handler, cookie := terminalHandler(t, c.svc)
		var body struct {
			Features []string `json:"features"`
		}
		decode(t, call(t, handler, cookie, http.MethodGet, "/api/session", ""), &body)
		if strings.Join(body.Features, ",") != c.want {
			t.Fatalf("%s: features = %v", c.name, body.Features)
		}
		mode := httptest.NewRecorder()
		handler.ServeHTTP(mode, httptest.NewRequest(http.MethodGet, "/api/mode", nil))
		if strings.Contains(mode.Body.String(), "terminal") {
			t.Fatalf("%s: /api/mode reports the terminal", c.name)
		}
		if c.svc == nil {
			if r := call(t, handler, cookie, http.MethodGet, "/api/terminal/spaces", ""); r.Code != http.StatusNotFound {
				t.Fatalf("disabled terminal route answered %d", r.Code)
			}
		}
	}
}

func TestTerminalRoutesNeedASessionAndActAsIt(t *testing.T) {
	svc := &fakeTerminal{}
	handler, cookie := terminalHandler(t, svc)
	anon := httptest.NewRecorder()
	handler.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/terminal/spaces", nil))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous spaces = %d", anon.Code)
	}
	if r := call(t, handler, cookie, http.MethodGet, "/api/terminal/spaces", ""); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"commands":["shell","claude"]`) {
		t.Fatalf("spaces = %d %s", r.Code, r.Body.String())
	}
	if r := call(t, handler, cookie, http.MethodPost, "/api/terminal/terminals", `{"space":"web","command":"claude","cols":80,"rows":24}`); r.Code != http.StatusOK {
		t.Fatalf("start = %d %s", r.Code, r.Body.String())
	}
	// No field may choose the account or the command line.
	if r := call(t, handler, cookie, http.MethodPost, "/api/terminal/terminals", `{"space":"web","command":"shell","account":"bob"}`); r.Code != http.StatusBadRequest {
		t.Fatalf("start with an account field = %d", r.Code)
	}
	if r := call(t, handler, cookie, http.MethodDelete, "/api/terminal/terminals/box%2Ft7", ""); r.Code != http.StatusNotFound {
		t.Fatalf("kill on another machine = %d", r.Code)
	}
	if r := call(t, handler, cookie, http.MethodDelete, "/api/terminal/terminals/t9", ""); r.Code != http.StatusNotFound {
		t.Fatalf("kill unknown = %d", r.Code)
	}
	if r := call(t, handler, cookie, http.MethodDelete, "/api/terminal/terminals/t1", ""); r.Code != http.StatusNoContent {
		t.Fatalf("kill = %d", r.Code)
	}
	for _, account := range svc.accounts {
		if account == "" {
			t.Fatal("a route called the service with no account")
		}
	}
}

func wsURL(server *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + path
}

func TestTerminalAttachRoundTrip(t *testing.T) {
	stream := &fakeTerminalStream{out: make(chan []byte, 4)}
	svc := &fakeTerminal{stream: stream}
	handler, cookie := terminalHandler(t, svc)
	server := httptest.NewServer(handler)
	defer server.Close()

	header := http.Header{}
	header.Set("Cookie", cookie.Name+"="+cookie.Value)
	header.Set("Origin", server.URL)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(server, "/api/terminal/terminals/t1/attach?cols=100&rows=30"), header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	stream.out <- []byte("$ ")
	kind, msg, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(msg) != "$ " {
		t.Fatalf("output = %d %q %v", kind, msg, err)
	}
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte("ls\r"))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"resize":[120,40]}`))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"resize":[0,40]}`))
	deadline := time.Now().Add(2 * time.Second)
	for {
		stream.mu.Lock()
		done := len(stream.input) == 1 && len(stream.resizes) == 1
		stream.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("input %v resizes %v", stream.input, stream.resizes)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stream.input[0] != "ls\r" || stream.resizes[0] != [2]uint16{120, 40} {
		t.Fatalf("input %v resizes %v", stream.input, stream.resizes)
	}
	close(stream.out)
	kind, msg, err = conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		t.Fatalf("close message = %d %q %v", kind, msg, err)
	}
	if string(msg) != `{"closed":{"reason":"exited","exit_code":0}}` {
		t.Fatalf("close message carried more than the reason: %s", msg)
	}
}

func TestTerminalAttachRefusesAnotherOriginAndUnknownTerminals(t *testing.T) {
	svc := &fakeTerminal{stream: &fakeTerminalStream{out: make(chan []byte)}}
	handler, cookie := terminalHandler(t, svc)
	server := httptest.NewServer(handler)
	defer server.Close()
	dial := func(path, origin string) int {
		header := http.Header{}
		header.Set("Cookie", cookie.Name+"="+cookie.Value)
		if origin != "" {
			header.Set("Origin", origin)
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL(server, path), header)
		if err == nil {
			conn.Close()
			return http.StatusSwitchingProtocols
		}
		if resp == nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if code := dial("/api/terminal/terminals/t1/attach", "https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-origin attach = %d", code)
	}
	if code := dial("/api/terminal/terminals/t1/attach", ""); code != http.StatusForbidden {
		t.Fatalf("attach without an origin = %d", code)
	}
	if code := dial("/api/terminal/terminals/t9/attach", server.URL); code != http.StatusNotFound {
		t.Fatalf("unknown terminal = %d", code)
	}
	if code := dial("/api/terminal/terminals/t1/attach", server.URL); code != http.StatusSwitchingProtocols {
		t.Fatalf("same-origin attach = %d", code)
	}
}
