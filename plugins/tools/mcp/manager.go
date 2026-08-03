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

// toolBreaker is one tool's consecutive-failure count. The breaker is
// per-tool rather than per-server so one broken tool cannot deny every other
// tool on the same server.
type toolBreaker struct {
	failures      int
	cooldownUntil time.Time
}

type serverRuntime struct {
	config  ServerConfig
	session clientSession
	status  ServerStatus
	// tools is this server's catalog as of the last successful discovery.
	// The flattened manager catalog is derived from it, so a reconnect or a
	// tools/list_changed notification only has to replace this slice.
	tools []ports.Tool
	// collisions is recomputed on every catalog rebuild, separately from the
	// discovery warnings in status, so a rebuild never duplicates or drops
	// the warnings discovery produced.
	collisions []string
	// protected holds the flattened names of this server's tools that config
	// requires an approval for. Keyed by flattened name because that is what a
	// caller has: the model calls "server__tool", not the remote name.
	protected map[string]bool
	breakers  map[string]*toolBreaker
	callMu    sync.Mutex
}

type Manager struct {
	mu       sync.RWMutex
	order    []string
	runtimes map[string]*serverRuntime
	tools    []ports.Tool
	// protected is the flattened catalog's approval-gated names, rebuilt with
	// the catalog so a reconnect can never drop a gate.
	protected map[string]bool
	now       func() time.Time
	oauth     map[string]*oauthProvider
	handlers  map[string]auth.OAuthHandler
	connect   connector
	http      *http.Client
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
	manager := &Manager{
		runtimes: map[string]*serverRuntime{},
		oauth:    map[string]*oauthProvider{},
		handlers: map[string]auth.OAuthHandler{},
		now:      options.Now,
		connect:  options.Connect,
		http:     options.HTTPClient,
	}
	for _, cfg := range configs {
		cfg = cfg.withDefaults()
		runtime := &serverRuntime{config: cfg, status: ServerStatus{Name: cfg.Name}, breakers: map[string]*toolBreaker{}}
		manager.runtimes[cfg.Name] = runtime
		manager.order = append(manager.order, cfg.Name)
		if !cfg.Enabled {
			runtime.status.State = StateDisabled
			continue
		}
		switch cfg.Auth {
		case "oauth":
			if options.OAuthStore == nil {
				runtime.status.State = StateUnavailable
				runtime.status.Diagnostic = "OAuth storage is unavailable"
				continue
			}
			provider := newOAuthProvider(cfg, options.OAuthStore, options.HTTPClient)
			manager.oauth[cfg.Name] = provider
			manager.handlers[cfg.Name] = provider
		case "bearer-env":
			manager.handlers[cfg.Name] = newBearerHandler(cfg.BearerToken)
		}
		session, remote, state, diagnostic := manager.connectServer(ctx, cfg)
		manager.applyServerLocked(runtime, session, remote, state, diagnostic)
	}
	slices.Sort(manager.order)
	manager.rebuildLocked()
	return manager, nil
}

// connectServer does all of one server's network work without holding the
// manager lock: the SDK can deliver a tools/list_changed notification while a
// session is being established, and that handler takes the same lock.
func (m *Manager) connectServer(ctx context.Context, cfg ServerConfig) (clientSession, []*sdk.Tool, ServerState, string) {
	m.mu.RLock()
	handler := m.handlers[cfg.Name]
	m.mu.RUnlock()
	if cfg.Auth == "oauth" && handler == nil {
		return nil, nil, StateUnavailable, "OAuth storage is unavailable"
	}
	opts := &sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}}
	opts.ToolListChangedHandler = func(handlerCtx context.Context, _ *sdk.ToolListChangedRequest) {
		m.onToolListChanged(handlerCtx, cfg.Name)
	}
	connectCtx, cancel := withTimeout(ctx, cfg.ConnectTimeout)
	session, err := m.connect(connectCtx, cfg, m.http, handler, opts)
	cancel()
	if err != nil {
		if errors.Is(err, ErrLoginRequired) {
			return nil, nil, StateLoginRequired, "login required"
		}
		return nil, nil, StateUnavailable, "connection failed"
	}
	remote, err := m.discover(ctx, cfg, session)
	if err != nil {
		// Closing here rather than keeping a session nothing can use is what
		// makes repeated reconnection safe.
		_ = session.Close()
		return nil, nil, StateUnavailable, "tool discovery failed"
	}
	return session, remote, StateReady, ""
}

func (m *Manager) discover(ctx context.Context, cfg ServerConfig, session clientSession) ([]*sdk.Tool, error) {
	discoveryCtx, cancel := withTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	return listAllTools(discoveryCtx, session)
}

// applyServerLocked installs one server's discovery outcome. It must be called
// with the manager lock held, or during construction where there is no
// concurrency yet.
func (m *Manager) applyServerLocked(runtime *serverRuntime, session clientSession, remote []*sdk.Tool, state ServerState, diagnostic string) {
	runtime.session = session
	runtime.status.State = state
	runtime.status.Diagnostic = diagnostic
	runtime.status.Warnings = nil
	runtime.status.ReloadRequired = false
	runtime.tools = nil
	runtime.protected = nil
	if state != StateReady {
		m.rebuildLocked()
		return
	}
	selected, warnings := filterTools(remote, runtime.config.Filter)
	runtime.status.Warnings = warnings
	tools := make([]ports.Tool, 0, len(selected))
	for _, advertised := range selected {
		// A tool Eggy cannot represent is skipped with a warning rather than
		// taken as proof the whole server is broken.
		tool, err := newRemoteTool(runtime.config.Name, advertised, m.sessionFor(runtime.config.Name), runtime.config.Timeout, runtime.config.MaxOutputBytes)
		if err != nil {
			runtime.status.Warnings = append(runtime.status.Warnings, fmt.Sprintf("tool %q was skipped: %v", advertised.Name, err))
			continue
		}
		tool.before = m.callGate(runtime.config.Name, advertised.Name)
		tool.onResult = m.resultHandler(runtime.config.Name, advertised.Name)
		if !runtime.config.SupportsParallelToolCalls {
			tool.executeMu = &runtime.callMu
		}
		if slices.Contains(runtime.config.RequireApproval, advertised.Name) {
			if runtime.protected == nil {
				runtime.protected = map[string]bool{}
			}
			runtime.protected[tool.definition.Name] = true
		}
		tools = append(tools, tool)
	}
	// A require_approval entry naming a tool this server does not advertise is
	// reported, not ignored. Silence would read as "the gate is on" while the
	// typo left it off, and the failure only surfaces once an unapproved call
	// has already run -- which is the whole thing the gate exists to prevent.
	for _, name := range runtime.config.RequireApproval {
		if !slices.ContainsFunc(selected, func(tool *sdk.Tool) bool { return tool != nil && tool.Name == name }) {
			runtime.status.Warnings = append(runtime.status.Warnings, fmt.Sprintf("require_approval names tool %q, which this server does not advertise", name))
		}
	}
	runtime.tools = tools
	m.rebuildLocked()
}

// rebuildLocked flattens every server's catalog into the one exported tool
// set. Servers are walked in a stable order so a name collision always
// resolves the same way: the first server owning the name keeps it, and every
// later claim is skipped with a warning rather than disabling that server.
func (m *Manager) rebuildLocked() {
	catalog := make([]ports.Tool, 0, len(m.tools))
	owners := map[string]string{}
	// Rebuilt with the catalog rather than kept alongside it: a tool that
	// dropped out of the catalog must drop its gate with it, and a tool that
	// came back through a reconnect must come back gated.
	protected := map[string]bool{}
	for _, name := range m.order {
		runtime := m.runtimes[name]
		if runtime == nil {
			continue
		}
		runtime.collisions = nil
		exported := 0
		for _, tool := range runtime.tools {
			toolName := tool.Definition().Name
			if owner, exists := owners[toolName]; exists {
				runtime.collisions = append(runtime.collisions, fmt.Sprintf("tool %q was skipped: name collision with server %q", toolName, owner))
				continue
			}
			owners[toolName] = name
			catalog = append(catalog, tool)
			if runtime.protected[toolName] {
				protected[toolName] = true
			}
			exported++
		}
		runtime.status.Tools = exported
	}
	m.protected = protected
	slices.SortFunc(catalog, func(left, right ports.Tool) int {
		if left.Definition().Name < right.Definition().Name {
			return -1
		}
		if left.Definition().Name > right.Definition().Name {
			return 1
		}
		return 0
	})
	m.tools = catalog
}

func (m *Manager) sessionFor(name string) func() clientSession {
	return func() clientSession {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if runtime := m.runtimes[name]; runtime != nil {
			return runtime.session
		}
		return nil
	}
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
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

// Tools is the live catalog. The agent loop reads it once per turn rather
// than snapshotting it at wiring time, so a reconnect, a logout, or a changed
// remote catalog takes effect on the next turn without a process restart.
func (m *Manager) Tools() []ports.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ports.Tool(nil), m.tools...)
}

// RequiresApproval reports whether a flattened catalog name is one config
// requires the owner to approve before it runs.
func (m *Manager) RequiresApproval(tool string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.protected[tool]
}

// HasApprovalGates reports whether any server configures require_approval at
// all. It lets bootstrap skip the gate entirely when nothing asks for it, so
// an unconfigured capability costs nothing at runtime.
func (m *Manager) HasApprovalGates() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, runtime := range m.runtimes {
		if len(runtime.config.RequireApproval) > 0 {
			return true
		}
	}
	return false
}

func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		statuses = append(statuses, runtime.snapshot())
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
	return runtime.snapshot(), nil
}

// Reconnect tears down any existing session and establishes a new one. It is
// the single repair path: /mcp reload, a completed login, a probe that found
// a dead session, and the call gate finding no session all route through it.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	m.mu.Lock()
	runtime, ok := m.runtimes[name]
	if !ok {
		m.mu.Unlock()
		return ErrServerNotFound
	}
	if !runtime.config.Enabled {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %q is disabled", name)
	}
	previous := runtime.session
	runtime.session = nil
	config := runtime.config
	m.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	session, remote, state, diagnostic := m.connectServer(ctx, config)
	m.mu.Lock()
	m.applyServerLocked(runtime, session, remote, state, diagnostic)
	m.mu.Unlock()
	if state != StateReady {
		return fmt.Errorf("MCP server %q is %s: %s", name, state, diagnostic)
	}
	return nil
}

// Refresh re-reads one server's tool catalog over its existing session, and
// falls back to a reconnect when there is no usable session. It is what makes
// a changed remote catalog visible without restarting the process.
func (m *Manager) Refresh(ctx context.Context, name string) error {
	m.mu.RLock()
	runtime, ok := m.runtimes[name]
	m.mu.RUnlock()
	if !ok {
		return ErrServerNotFound
	}
	if !runtime.config.Enabled {
		return fmt.Errorf("MCP server %q is disabled", name)
	}
	session := m.sessionFor(name)()
	if session == nil {
		return m.Reconnect(ctx, name)
	}
	remote, err := m.discover(ctx, runtime.config, session)
	if err != nil {
		return m.Reconnect(ctx, name)
	}
	m.mu.Lock()
	m.applyServerLocked(runtime, session, remote, StateReady, "")
	m.mu.Unlock()
	return nil
}

// Disconnect drops one server's session and its tools from the live catalog
// without touching any other server. Logging out uses it so removing one
// server's credentials no longer requires restarting Eggy.
func (m *Manager) Disconnect(name string, state ServerState, diagnostic string) error {
	m.mu.Lock()
	runtime, ok := m.runtimes[name]
	if !ok {
		m.mu.Unlock()
		return ErrServerNotFound
	}
	previous := runtime.session
	m.applyServerLocked(runtime, nil, nil, state, diagnostic)
	m.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

func (m *Manager) Probe(ctx context.Context, name string) (ProbeResult, error) {
	m.mu.RLock()
	runtime, ok := m.runtimes[name]
	m.mu.RUnlock()
	if !ok {
		return ProbeResult{}, ErrServerNotFound
	}
	status := runtime.snapshotLocking(m)
	probe := ProbeResult{Server: name, State: status.State, Diagnostic: status.Diagnostic}
	if !runtime.config.Enabled {
		return probe, nil
	}
	started := m.now()
	session := m.sessionFor(name)()
	var err error
	if session != nil {
		_, err = m.discover(ctx, runtime.config, session)
	}
	// A probe repairs what it diagnoses: a server that never connected or
	// whose session died is reconnected here rather than reported as broken
	// until someone restarts the process.
	if session == nil || err != nil {
		if reconnectErr := m.Reconnect(ctx, name); reconnectErr != nil {
			status = runtime.snapshotLocking(m)
			probe.State, probe.Diagnostic, probe.Tools = status.State, status.Diagnostic, 0
			probe.Latency = m.now().Sub(started)
			return probe, nil
		}
	}
	status = runtime.snapshotLocking(m)
	probe.Latency = m.now().Sub(started)
	probe.State = status.State
	probe.Diagnostic = status.Diagnostic
	probe.Tools = status.Tools
	return probe, nil
}

func (m *Manager) BeginLogin(ctx context.Context, name string) (string, error) {
	provider, err := m.provider(name)
	if err != nil {
		return "", err
	}
	return provider.BeginLogin(ctx)
}

// CompleteLogin connects the server as soon as its credentials exist, so a
// finished OAuth flow makes the server's tools available on the next turn.
func (m *Manager) CompleteLogin(ctx context.Context, name, code, state string) error {
	provider, err := m.provider(name)
	if err != nil {
		return err
	}
	if err := provider.CompleteLogin(ctx, code, state); err != nil {
		return err
	}
	return m.Reconnect(ctx, name)
}

func (m *Manager) Logout(name string) error {
	provider, err := m.provider(name)
	if err != nil {
		return err
	}
	if err := provider.Logout(); err != nil {
		return err
	}
	return m.Disconnect(name, StateLoginRequired, "login required")
}

func (m *Manager) provider(name string) (*oauthProvider, error) {
	m.mu.RLock()
	provider := m.oauth[name]
	_, configured := m.runtimes[name]
	m.mu.RUnlock()
	if !configured {
		return nil, ErrServerNotFound
	}
	if provider == nil {
		return nil, errors.New("MCP server does not use OAuth")
	}
	return provider, nil
}

func (r *serverRuntime) snapshot() ServerStatus {
	status := r.status
	warnings := make([]string, 0, len(r.status.Warnings)+len(r.collisions))
	warnings = append(warnings, r.status.Warnings...)
	warnings = append(warnings, r.collisions...)
	if len(warnings) == 0 {
		warnings = nil
	}
	status.Warnings = warnings
	return status
}

func (r *serverRuntime) snapshotLocking(m *Manager) ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return r.snapshot()
}

// MarkReloadRequired records that a server's catalog is known to be stale.
// It is now only reached when an automatic refresh failed, since a
// tools/list_changed notification normally refreshes in place.
func (m *Manager) MarkReloadRequired(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runtime := m.runtimes[name]; runtime != nil {
		runtime.status.ReloadRequired = true
	}
}

func (m *Manager) onToolListChanged(ctx context.Context, name string) {
	if err := m.Refresh(ctx, name); err != nil {
		m.MarkReloadRequired(name)
	}
}

// callGate runs before every remote call. It enforces the calling tool's own
// cooldown and re-establishes the session when one is missing or when a
// cooldown just expired. Reconnecting on expiry is the repair the old gate
// lacked: a server whose session died came back ready and failed forever,
// because nothing ever rebuilt the session it was failing on.
func (m *Manager) callGate(server, tool string) func(context.Context) error {
	return func(ctx context.Context) error {
		m.mu.Lock()
		runtime := m.runtimes[server]
		if runtime == nil {
			m.mu.Unlock()
			return ErrServerNotFound
		}
		now := m.now()
		recovered := false
		// A breaker with no cooldown is a run of failures that has not reached
		// the threshold yet, and must keep its count.
		if breaker := runtime.breakers[tool]; breaker != nil && !breaker.cooldownUntil.IsZero() {
			if now.Before(breaker.cooldownUntil) {
				remaining := breaker.cooldownUntil.Sub(now).Round(time.Second)
				m.mu.Unlock()
				return fmt.Errorf("MCP tool %q is cooling down for another %s", tool, remaining)
			}
			recovered = true
			delete(runtime.breakers, tool)
			m.refreshStateLocked(runtime)
		}
		reconnect := runtime.config.Enabled && (runtime.session == nil || recovered)
		m.mu.Unlock()
		if reconnect {
			return m.Reconnect(ctx, server)
		}
		return nil
	}
}

// resultHandler counts one tool's consecutive failures. Both the threshold
// and the cooldown are per-server configuration; the count is per tool.
func (m *Manager) resultHandler(server, tool string) func(error) {
	return func(err error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		runtime := m.runtimes[server]
		if runtime == nil {
			return
		}
		if err == nil {
			delete(runtime.breakers, tool)
			m.refreshStateLocked(runtime)
			return
		}
		breaker := runtime.breakers[tool]
		if breaker == nil {
			breaker = &toolBreaker{}
			runtime.breakers[tool] = breaker
		}
		breaker.failures++
		if breaker.failures >= runtime.config.FailureThreshold {
			breaker.cooldownUntil = m.now().Add(runtime.config.Cooldown)
			runtime.status.State = StateCooldown
			runtime.status.Diagnostic = fmt.Sprintf("tool %q failed %d consecutive calls", tool, breaker.failures)
		}
	}
}

// refreshStateLocked returns a server to ready once no tool is cooling down.
// It never overwrites a state that describes the connection itself.
func (m *Manager) refreshStateLocked(runtime *serverRuntime) {
	if runtime.status.State != StateCooldown {
		return
	}
	now := m.now()
	for name, breaker := range runtime.breakers {
		if now.Before(breaker.cooldownUntil) {
			return
		}
		if breaker.failures == 0 {
			delete(runtime.breakers, name)
		}
	}
	runtime.status.State = StateReady
	runtime.status.Diagnostic = ""
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
			runtime.session = nil
		}
	}
	return first
}
