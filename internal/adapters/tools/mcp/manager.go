package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nigelteosw/eggy/internal/ports"
)

type serverRuntime struct {
	config        ServerConfig
	session       clientSession
	status        ServerStatus
	failures      int
	cooldownUntil time.Time
	callMu        sync.Mutex
}

type Manager struct {
	mu       sync.RWMutex
	runtimes map[string]*serverRuntime
	tools    []ports.Tool
	now      func() time.Time
	oauth    map[string]*oauthProvider
}

func NewManager(ctx context.Context, configs []ServerConfig, options Options) (*Manager, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Connect == nil {
		options.Connect = connectSDK
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	manager := &Manager{runtimes: map[string]*serverRuntime{}, oauth: map[string]*oauthProvider{}, now: options.Now}
	projected := map[string]string{}
	for _, cfg := range configs {
		runtime := &serverRuntime{config: cfg, status: ServerStatus{Name: cfg.Name}}
		manager.runtimes[cfg.Name] = runtime
		if !cfg.Enabled {
			runtime.status.State = StateDisabled
			continue
		}
		connectCtx := ctx
		cancel := func() {}
		if cfg.ConnectTimeout > 0 {
			connectCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		}
		opts := &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}}
		opts.ToolListChangedHandler = func(context.Context, *sdk.ToolListChangedRequest) {
			manager.MarkReloadRequired(cfg.Name)
		}
		var handler auth.OAuthHandler
		switch cfg.Auth {
		case "oauth":
			if options.OAuthStore == nil {
				cancel()
				runtime.status.State = StateUnavailable
				runtime.status.Diagnostic = "OAuth storage is unavailable"
				continue
			}
			provider := newOAuthProvider(cfg, options.OAuthStore, options.HTTPClient)
			manager.oauth[cfg.Name] = provider
			handler = provider
		case "bearer-env":
			handler = newBearerHandler(cfg.BearerToken)
		}
		session, err := options.Connect(connectCtx, cfg, options.HTTPClient, handler, opts)
		cancel()
		if err != nil {
			if errors.Is(err, ErrLoginRequired) {
				runtime.status.State = StateLoginRequired
				runtime.status.Diagnostic = "login required"
			} else {
				runtime.status.State = StateUnavailable
				runtime.status.Diagnostic = "connection failed"
			}
			continue
		}
		runtime.session = session
		discoveryCtx := ctx
		discoveryCancel := func() {}
		if cfg.ConnectTimeout > 0 {
			discoveryCtx, discoveryCancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		}
		remoteTools, err := listAllTools(discoveryCtx, session)
		discoveryCancel()
		if err != nil {
			runtime.status.State = StateUnavailable
			runtime.status.Diagnostic = "tool discovery failed"
			continue
		}
		selected, warnings := filterTools(remoteTools, cfg.Filter)
		runtime.status.Warnings = warnings
		serverTools := make([]ports.Tool, 0, len(selected))
		for _, remote := range selected {
			tool, err := newRemoteTool(cfg.Name, remote, session, cfg.Timeout, cfg.MaxOutputBytes, manager.resultHandler(cfg.Name))
			if err != nil {
				runtime.status.State = StateUnavailable
				runtime.status.Diagnostic = "invalid tool catalog"
				serverTools = nil
				break
			}
			tool.before = manager.callGate(cfg.Name)
			if !cfg.SupportsParallelToolCalls {
				tool.executeMu = &runtime.callMu
			}
			name := tool.Definition().Name
			if owner, exists := projected[name]; exists {
				runtime.status.State = StateUnavailable
				runtime.status.Diagnostic = fmt.Sprintf("tool name collision with server %q", owner)
				serverTools = nil
				break
			}
			projected[name] = cfg.Name
			serverTools = append(serverTools, tool)
		}
		if serverTools == nil {
			continue
		}
		runtime.status.State = StateReady
		runtime.status.Tools = len(serverTools)
		manager.tools = append(manager.tools, serverTools...)
	}
	slices.SortFunc(manager.tools, func(left, right ports.Tool) int {
		if left.Definition().Name < right.Definition().Name {
			return -1
		}
		if left.Definition().Name > right.Definition().Name {
			return 1
		}
		return 0
	})
	return manager, nil
}

func listAllTools(ctx context.Context, session clientSession) ([]*sdk.Tool, error) {
	var tools []*sdk.Tool
	cursor := ""
	for {
		result, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("empty tools result")
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
}

func filterTools(tools []*sdk.Tool, filter ToolFilter) ([]*sdk.Tool, []string) {
	advertised := map[string]*sdk.Tool{}
	for _, tool := range tools {
		if tool != nil {
			advertised[tool.Name] = tool
		}
	}
	included := map[string]bool{}
	if len(filter.Include) == 0 {
		for name := range advertised {
			included[name] = true
		}
	} else {
		for _, name := range filter.Include {
			included[name] = true
		}
	}
	for _, name := range filter.Exclude {
		delete(included, name)
	}
	var selected []*sdk.Tool
	var warnings []string
	for _, name := range filter.Include {
		if _, ok := advertised[name]; !ok {
			warnings = append(warnings, fmt.Sprintf("configured tool %q was not advertised", name))
		}
	}
	for name := range included {
		if tool, ok := advertised[name]; ok {
			selected = append(selected, tool)
		}
	}
	slices.SortFunc(selected, func(left, right *sdk.Tool) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return selected, warnings
}

func (m *Manager) Tools() []ports.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ports.Tool(nil), m.tools...)
}

func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		statuses = append(statuses, cloneStatus(runtime.status))
	}
	slices.SortFunc(statuses, func(left, right ServerStatus) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return statuses
}

func (m *Manager) Status(name string) (ServerStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtime, ok := m.runtimes[name]
	if !ok {
		return ServerStatus{}, ErrServerNotFound
	}
	return cloneStatus(runtime.status), nil
}

func (m *Manager) Probe(ctx context.Context, name string) (ProbeResult, error) {
	m.mu.RLock()
	runtime, ok := m.runtimes[name]
	if !ok {
		m.mu.RUnlock()
		return ProbeResult{}, ErrServerNotFound
	}
	state := runtime.status.State
	diagnostic := runtime.status.Diagnostic
	session := runtime.session
	m.mu.RUnlock()
	probe := ProbeResult{Server: name, State: state, Diagnostic: diagnostic}
	if session == nil {
		return probe, nil
	}
	started := m.now()
	probeCtx := ctx
	cancel := func() {}
	if runtime.config.ConnectTimeout > 0 {
		probeCtx, cancel = context.WithTimeout(ctx, runtime.config.ConnectTimeout)
	}
	tools, err := listAllTools(probeCtx, session)
	cancel()
	probe.Latency = m.now().Sub(started)
	if err != nil {
		probe.State = StateUnavailable
		probe.Diagnostic = "tool discovery failed"
		return probe, nil
	}
	selected, _ := filterTools(tools, runtime.config.Filter)
	probe.State = StateReady
	probe.Tools = len(selected)
	probe.Diagnostic = ""
	return probe, nil
}

func (m *Manager) BeginLogin(ctx context.Context, name string) (string, error) {
	m.mu.RLock()
	provider := m.oauth[name]
	_, configured := m.runtimes[name]
	m.mu.RUnlock()
	if !configured {
		return "", ErrServerNotFound
	}
	if provider == nil {
		return "", errors.New("MCP server does not use OAuth")
	}
	return provider.BeginLogin(ctx)
}

func (m *Manager) CompleteLogin(ctx context.Context, name, code, state string) error {
	m.mu.RLock()
	provider := m.oauth[name]
	_, configured := m.runtimes[name]
	m.mu.RUnlock()
	if !configured {
		return ErrServerNotFound
	}
	if provider == nil {
		return errors.New("MCP server does not use OAuth")
	}
	return provider.CompleteLogin(ctx, code, state)
}

func (m *Manager) Logout(name string) error {
	m.mu.RLock()
	provider := m.oauth[name]
	_, configured := m.runtimes[name]
	m.mu.RUnlock()
	if !configured {
		return ErrServerNotFound
	}
	if provider == nil {
		return errors.New("MCP server does not use OAuth")
	}
	return provider.Logout()
}

func cloneStatus(status ServerStatus) ServerStatus {
	status.Warnings = append([]string(nil), status.Warnings...)
	return status
}

func (m *Manager) MarkReloadRequired(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runtime := m.runtimes[name]; runtime != nil {
		runtime.status.ReloadRequired = true
	}
}

func (m *Manager) callGate(name string) func() error {
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		runtime := m.runtimes[name]
		if runtime == nil || runtime.status.State != StateCooldown {
			return nil
		}
		if !m.now().Before(runtime.cooldownUntil) {
			runtime.failures = 0
			runtime.status.State = StateReady
			runtime.status.Diagnostic = ""
			return nil
		}
		return fmt.Errorf("MCP server %q is cooling down", name)
	}
}

func (m *Manager) resultHandler(name string) func(error) {
	return func(err error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		runtime := m.runtimes[name]
		if runtime == nil {
			return
		}
		if err == nil {
			runtime.failures = 0
			return
		}
		runtime.failures++
		if runtime.failures >= 3 {
			runtime.status.State = StateCooldown
			runtime.status.Diagnostic = "three consecutive tool calls failed"
			runtime.cooldownUntil = m.now().Add(30 * time.Second)
		}
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for _, runtime := range m.runtimes {
		if runtime.session != nil {
			if err := runtime.session.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}
