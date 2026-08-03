package mcp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestManagerFiltersAndIsolatesServers(t *testing.T) {
	sessions := map[string]*fakeSession{
		"ready":  {tools: []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}, {Name: "secret", InputSchema: objectSchema()}}},
		"broken": {listErr: errors.New("offline with Authorization: Bearer secret")},
	}
	manager, err := NewManager(context.Background(), []ServerConfig{
		{Name: "ready", URL: "https://ready.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096, Filter: ToolFilter{Include: []string{"read", "missing"}}},
		{Name: "broken", URL: "https://broken.example", Enabled: true},
		{Name: "disabled", URL: "https://disabled.example", Enabled: false},
	}, Options{Connect: sessionConnector(sessions), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"ready__read"}) {
		t.Fatalf("tools=%v", names)
	}
	ready, err := manager.Status("ready")
	if err != nil || ready.State != StateReady || ready.Tools != 1 || !slices.Contains(ready.Warnings, `configured tool "missing" was not advertised`) {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	broken, err := manager.Status("broken")
	if err != nil || broken.State != StateUnavailable || broken.Diagnostic == "" || containsSecret(broken.Diagnostic) {
		t.Fatalf("broken=%#v err=%v", broken, err)
	}
	disabled, err := manager.Status("disabled")
	if err != nil || disabled.State != StateDisabled {
		t.Fatalf("disabled=%#v err=%v", disabled, err)
	}
}

func TestManagerListsEveryPageAndExclusionsWin(t *testing.T) {
	session := &fakeSession{pages: map[string]*sdk.ListToolsResult{
		"":     {Tools: []*sdk.Tool{{Name: "first", InputSchema: objectSchema()}}, NextCursor: "next"},
		"next": {Tools: []*sdk.Tool{{Name: "second", InputSchema: objectSchema()}, {Name: "blocked", InputSchema: objectSchema()}}},
	}}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
		Filter: ToolFilter{Include: []string{"first", "second", "blocked"}, Exclude: []string{"blocked"}},
	}}, Options{Connect: sessionConnector(map[string]*fakeSession{"example": session}), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"example__first", "example__second"}) {
		t.Fatalf("tools=%v", names)
	}
}

func TestManagerEntersCooldownAfterThreeCallFailures(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	session := &fakeSession{
		tools:      []*sdk.Tool{{Name: "unstable", InputSchema: objectSchema()}},
		callResult: &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}},
		callErr:    errors.New("remote failure"),
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096}}, Options{
		Connect: sessionConnector(map[string]*fakeSession{"example": session}), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tool := manager.Tools()[0]
	for range 3 {
		if _, err := tool.Execute(context.Background(), nil); err == nil {
			t.Fatal("expected remote failure")
		}
	}
	status, _ := manager.Status("example")
	if status.State != StateCooldown {
		t.Fatalf("status=%#v", status)
	}
	if _, err := tool.Execute(context.Background(), nil); err == nil || session.callCount != 3 {
		t.Fatalf("cooldown call reached server: calls=%d err=%v", session.callCount, err)
	}
	now = now.Add(31 * time.Second)
	session.callErr = nil
	if _, err := tool.Execute(context.Background(), nil); err != nil || session.callCount != 4 {
		t.Fatalf("call after cooldown: calls=%d err=%v", session.callCount, err)
	}
}

func TestManagerSerializesCallsUnlessServerAllowsParallelism(t *testing.T) {
	session := &blockingSession{started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	connect := func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		return session, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{Name: "serial", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096}}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tool := manager.Tools()[0]
	var calls sync.WaitGroup
	calls.Add(2)
	for range 2 {
		go func() {
			defer calls.Done()
			_, _ = tool.Execute(context.Background(), nil)
		}()
	}
	<-session.started
	select {
	case <-session.started:
		t.Fatal("second call started before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	session.release <- struct{}{}
	select {
	case <-session.started:
	case <-time.After(time.Second):
		t.Fatal("second serialized call did not start")
	}
	session.release <- struct{}{}
	calls.Wait()
}

func TestManagerProbeAndToolListChangeRefreshInPlace(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}}}
	var clientOptions *sdk.ClientOptions
	connect := func(_ context.Context, _ ServerConfig, _ *http.Client, _ auth.OAuthHandler, options *sdk.ClientOptions) (clientSession, error) {
		clientOptions = options
		return session, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096}}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	probe, err := manager.Probe(context.Background(), "example")
	if err != nil || probe.State != StateReady || probe.Tools != 1 {
		t.Fatalf("probe=%#v err=%v", probe, err)
	}
	session.tools = append(session.tools, &sdk.Tool{Name: "write", InputSchema: objectSchema()})
	clientOptions.ToolListChangedHandler(context.Background(), nil)
	status, _ := manager.Status("example")
	if status.ReloadRequired || status.Tools != 2 {
		t.Fatalf("status=%#v", status)
	}
	if names := managerToolNames(manager); !slices.Equal(names, []string{"example__read", "example__write"}) {
		t.Fatalf("tools=%v", names)
	}
}

func TestManagerReconnectsServerThatWasDownAtBoot(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}}}
	online := false
	connect := func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		if !online {
			return nil, errors.New("connection refused")
		}
		return session, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096}}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); len(names) != 0 {
		t.Fatalf("tools before reconnect=%v", names)
	}
	if err := manager.Reconnect(context.Background(), "example"); err == nil {
		t.Fatal("expected reconnect to a refusing server to fail")
	}
	online = true
	if err := manager.Reconnect(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	if names := managerToolNames(manager); !slices.Equal(names, []string{"example__read"}) {
		t.Fatalf("tools after reconnect=%v", names)
	}
}

// A session that dies mid-life used to be unrecoverable: the breaker returned
// the server to ready and every later call failed on the same dead session.
func TestManagerRebuildsSessionWhenCooldownExpires(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	dead := &fakeSession{
		tools:   []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}},
		callErr: errors.New("session closed"),
	}
	live := &fakeSession{
		tools:      []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}},
		callResult: &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}},
	}
	current := clientSession(dead)
	connect := func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		return current, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096, FailureThreshold: 2, Cooldown: 10 * time.Second}}, Options{
		Connect: connect, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tool := manager.Tools()[0]
	for range 2 {
		if _, err := tool.Execute(context.Background(), nil); err == nil {
			t.Fatal("expected the dead session to fail the call")
		}
	}
	if status, _ := manager.Status("example"); status.State != StateCooldown {
		t.Fatalf("status=%#v", status)
	}
	now = now.Add(11 * time.Second)
	current = live
	if _, err := tool.Execute(context.Background(), nil); err != nil {
		t.Fatalf("call after cooldown: %v", err)
	}
	if !dead.closed || live.callCount != 1 {
		t.Fatalf("dead.closed=%t live.callCount=%d", dead.closed, live.callCount)
	}
	if status, _ := manager.Status("example"); status.State != StateReady {
		t.Fatalf("status=%#v", status)
	}
}

// One tool's failures must not deny the other tools on the same server.
func TestManagerCooldownIsPerTool(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	session := &failingToolSession{broken: "unstable", result: &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "example", URL: "https://mcp.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
		SupportsParallelToolCalls: true, FailureThreshold: 2, Cooldown: 10 * time.Second,
	}}, Options{Connect: func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		return session, nil
	}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tools := map[string]ports.Tool{}
	for _, tool := range manager.Tools() {
		tools[tool.Definition().Name] = tool
	}
	for range 2 {
		if _, err := tools["example__unstable"].Execute(context.Background(), nil); err == nil {
			t.Fatal("expected the broken tool to fail")
		}
	}
	if _, err := tools["example__unstable"].Execute(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("broken tool err=%v", err)
	}
	if _, err := tools["example__healthy"].Execute(context.Background(), nil); err != nil {
		t.Fatalf("healthy tool was denied by another tool's breaker: %v", err)
	}
}

// A colliding tool name costs that one tool, not the whole server.
func TestManagerSkipsCollidingToolAndKeepsServerReady(t *testing.T) {
	sessions := map[string]*fakeSession{
		"alpha": {tools: []*sdk.Tool{{Name: "shared", InputSchema: objectSchema()}}},
		"beta":  {tools: []*sdk.Tool{{Name: "shared", InputSchema: objectSchema()}, {Name: "own", InputSchema: objectSchema()}}},
	}
	manager, err := NewManager(context.Background(), []ServerConfig{
		{Name: "alpha", URL: "https://alpha.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096},
		// beta projects the same name as alpha, because normalization is per
		// server and both servers are named so the projected names collide.
		{Name: "alpha_", URL: "https://beta.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096},
	}, Options{Connect: func(_ context.Context, cfg ServerConfig, _ *http.Client, _ auth.OAuthHandler, _ *sdk.ClientOptions) (clientSession, error) {
		if cfg.Name == "alpha" {
			return sessions["alpha"], nil
		}
		return sessions["beta"], nil
	}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"alpha__own", "alpha__shared"}) {
		t.Fatalf("tools=%v", names)
	}
	second, _ := manager.Status("alpha_")
	if second.State != StateReady || second.Tools != 1 {
		t.Fatalf("second server was disabled by a collision: %#v", second)
	}
	if !slices.ContainsFunc(second.Warnings, func(warning string) bool { return strings.Contains(warning, "collision") }) {
		t.Fatalf("warnings=%v", second.Warnings)
	}
}

// Logging out removes one server's tools from the live catalog; every other
// server keeps working, and no restart is involved.
func TestManagerLogoutDropsOnlyThatServersTools(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	sessions := map[string]*fakeSession{
		"railway": {tools: []*sdk.Tool{{Name: "deploy", InputSchema: objectSchema()}}},
		"other":   {tools: []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}}},
	}
	manager, err := NewManager(context.Background(), []ServerConfig{
		{Name: "railway", URL: "https://railway.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback", Auth: "oauth", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096},
		{Name: "other", URL: "https://other.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096},
	}, Options{Connect: sessionConnector(sessions), OAuthStore: store, HTTPClient: &http.Client{Transport: &oauthRoundTripper{}}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"other__read", "railway__deploy"}) {
		t.Fatalf("tools=%v", names)
	}
	if err := manager.Logout("railway"); err != nil {
		t.Fatal(err)
	}
	if names := managerToolNames(manager); !slices.Equal(names, []string{"other__read"}) {
		t.Fatalf("tools after logout=%v", names)
	}
	status, _ := manager.Status("railway")
	if status.State != StateLoginRequired || !sessions["railway"].closed {
		t.Fatalf("status=%#v closed=%t", status, sessions["railway"].closed)
	}
}

type failingToolSession struct {
	broken string
	result *sdk.CallToolResult
}

func (s *failingToolSession) ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	return &sdk.ListToolsResult{Tools: []*sdk.Tool{
		{Name: "healthy", InputSchema: objectSchema()},
		{Name: s.broken, InputSchema: objectSchema()},
	}}, nil
}

func (s *failingToolSession) CallTool(_ context.Context, params *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	if params.Name == s.broken {
		return nil, errors.New("tool failure")
	}
	return s.result, nil
}

func (s *failingToolSession) Close() error { return nil }

func TestNewFakeManagerProjectsConfiguredIncludes(t *testing.T) {
	manager, err := NewFakeManager([]ServerConfig{
		{Name: "railway", Enabled: true, Filter: ToolFilter{Include: []string{"list-projects", "get-logs"}}},
		{Name: "off", Enabled: false, Filter: ToolFilter{Include: []string{"ignored"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"railway__get_logs", "railway__list_projects"}) {
		t.Fatalf("tools=%v", names)
	}
}

func TestManagerMarksOAuthServerLoginRequiredAndCanBeginLogin(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	client := &http.Client{Transport: &oauthRoundTripper{}}
	connect := func(ctx context.Context, _ ServerConfig, _ *http.Client, handler auth.OAuthHandler, _ *sdk.ClientOptions) (clientSession, error) {
		tokenSource, err := handler.TokenSource(ctx)
		if err != nil {
			return nil, err
		}
		if tokenSource == nil {
			return nil, ErrLoginRequired
		}
		return nil, errors.New("unexpected token")
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "railway", URL: "https://resource.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback", Auth: "oauth", Enabled: true,
	}}, Options{Connect: connect, HTTPClient: client, OAuthStore: store, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := manager.Status("railway")
	if status.State != StateLoginRequired {
		t.Fatalf("status=%#v", status)
	}
	authorizationURL, err := manager.BeginLogin(context.Background(), "railway")
	if err != nil || !strings.HasPrefix(authorizationURL, "https://auth.example/authorize?") {
		t.Fatalf("authorization URL=%q err=%v", authorizationURL, err)
	}
}

func TestManagerBoundsInitialToolDiscovery(t *testing.T) {
	session := &deadlineSession{}
	connect := func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		return session, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "bounded", URL: "https://mcp.example", Enabled: true, ConnectTimeout: time.Second, Timeout: time.Second, MaxOutputBytes: 4096,
	}}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, _ := manager.Status("bounded")
	if status.State != StateReady || !session.sawDeadline {
		t.Fatalf("status=%#v sawDeadline=%t", status, session.sawDeadline)
	}
}

type deadlineSession struct{ sawDeadline bool }

func (s *deadlineSession) ListTools(ctx context.Context, _ *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	_, s.sawDeadline = ctx.Deadline()
	if !s.sawDeadline {
		return nil, errors.New("missing discovery deadline")
	}
	return &sdk.ListToolsResult{}, nil
}
func (*deadlineSession) CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	return &sdk.CallToolResult{}, nil
}
func (*deadlineSession) Close() error { return nil }

type blockingSession struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingSession) ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	return &sdk.ListToolsResult{Tools: []*sdk.Tool{{Name: "call", InputSchema: objectSchema()}}}, nil
}
func (s *blockingSession) CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	s.started <- struct{}{}
	<-s.release
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
}
func (s *blockingSession) Close() error { return nil }

func objectSchema() map[string]any { return map[string]any{"type": "object"} }

func sessionConnector(sessions map[string]*fakeSession) connector {
	return func(_ context.Context, cfg ServerConfig, _ *http.Client, _ auth.OAuthHandler, _ *sdk.ClientOptions) (clientSession, error) {
		session, ok := sessions[cfg.Name]
		if !ok {
			return nil, errors.New("connect failed")
		}
		return session, nil
	}
}

func managerToolNames(manager *Manager) []string {
	names := make([]string, 0)
	for _, tool := range manager.Tools() {
		names = append(names, tool.Definition().Name)
	}
	return names
}

func containsSecret(value string) bool {
	return strings.Contains(value, "Bearer") || strings.Contains(value, "secret")
}

// require_approval marks the flattened name, because that is what the model
// calls and what the gate has to recognize.
func TestManagerMarksConfiguredToolsAsRequiringApproval(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{
		{Name: "send-message", InputSchema: objectSchema()},
		{Name: "list-messages", InputSchema: objectSchema()},
	}}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "mail", URL: "https://mail.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
		RequireApproval: []string{"send-message"},
	}}, Options{Connect: sessionConnector(map[string]*fakeSession{"mail": session}), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if !manager.RequiresApproval("mail__send_message") {
		t.Fatal("configured tool is not gated")
	}
	if manager.RequiresApproval("mail__list_messages") {
		t.Fatal("an unconfigured tool was gated")
	}
	if !manager.HasApprovalGates() {
		t.Fatal("manager does not report configured gates")
	}
}

// A server with no require_approval costs nothing: bootstrap skips the wrapper
// and the executor entirely on the strength of this answer.
func TestManagerWithoutRequireApprovalHasNoGates(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{{Name: "send-message", InputSchema: objectSchema()}}}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "mail", URL: "https://mail.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
	}}, Options{Connect: sessionConnector(map[string]*fakeSession{"mail": session}), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.HasApprovalGates() || manager.RequiresApproval("mail__send_message") {
		t.Fatal("an unconfigured server reported a gate")
	}
}

// A typo in require_approval leaves a tool ungated, and the only thing that
// would ever reveal it is a warning: the failure otherwise surfaces once an
// unapproved call has already run.
func TestManagerWarnsWhenRequireApprovalNamesAnUnadvertisedTool(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{{Name: "send-message", InputSchema: objectSchema()}}}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "mail", URL: "https://mail.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
		RequireApproval: []string{"send_message"},
	}}, Options{Connect: sessionConnector(map[string]*fakeSession{"mail": session}), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status, err := manager.Status("mail")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(status.Warnings, `require_approval names tool "send_message", which this server does not advertise`) {
		t.Fatalf("no warning for an unmatched require_approval entry: %#v", status.Warnings)
	}
}

// A catalog rebuild must not un-gate a tool. The gate is derived from config
// on every rebuild rather than stamped once at startup, because a server that
// reconnects mid-conversation replaces every tool object it exported.
func TestManagerKeepsApprovalGateAcrossACatalogRebuild(t *testing.T) {
	session := &fakeSession{tools: []*sdk.Tool{{Name: "send-message", InputSchema: objectSchema()}}}
	var clientOptions *sdk.ClientOptions
	connect := func(_ context.Context, _ ServerConfig, _ *http.Client, _ auth.OAuthHandler, options *sdk.ClientOptions) (clientSession, error) {
		clientOptions = options
		return session, nil
	}
	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "mail", URL: "https://mail.example", Enabled: true, Timeout: time.Second, MaxOutputBytes: 4096,
		RequireApproval: []string{"send-message"},
	}}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	session.tools = append(session.tools, &sdk.Tool{Name: "list-messages", InputSchema: objectSchema()})
	clientOptions.ToolListChangedHandler(context.Background(), nil)
	if names := managerToolNames(manager); !slices.Equal(names, []string{"mail__list_messages", "mail__send_message"}) {
		t.Fatalf("tools=%v", names)
	}
	if !manager.RequiresApproval("mail__send_message") {
		t.Fatal("a catalog rebuild dropped the approval gate")
	}
	if manager.RequiresApproval("mail__list_messages") {
		t.Fatal("a rebuild gated a tool config never named")
	}
}
