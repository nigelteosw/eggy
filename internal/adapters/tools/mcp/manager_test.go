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

func TestManagerProbeAndToolListChangeStatus(t *testing.T) {
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
	clientOptions.ToolListChangedHandler(context.Background(), nil)
	status, _ := manager.Status("example")
	if !status.ReloadRequired {
		t.Fatalf("status=%#v", status)
	}
}

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
	store, _ := OpenOAuthStore(t.TempDir(), testEncryptionKey())
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
