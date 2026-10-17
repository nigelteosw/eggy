package mcp

import (
	"context"
	"errors"
	"fmt"
)

// ErrRestartRequired means the server is not in the running manager: it was
// added to config after boot. The owner restarts, and the server can then be
// previewed.
var ErrRestartRequired = errors.New("restart to load this server")

// ToolPreview is one tool a server advertises, shown before the owner decides
// which of them need approval.
type ToolPreview struct {
	Name        string
	Description string
}

// Preview connects to a configured server once and lists the tools it
// advertises, whether or not the server is enabled. Nothing is registered and
// the running catalog and status are left as they were, so previewing a
// server the owner has not enabled changes no behaviour.
func (m *Manager) Preview(ctx context.Context, name string) ([]ToolPreview, error) {
	m.mu.RLock()
	runtime, ok := m.runtimes[name]
	handler := m.handlers[name]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrRestartRequired
	}
	cfg := runtime.config
	if cfg.Auth == "oauth" && handler == nil {
		// A server disabled at boot never had its OAuth handler built, because
		// construction skips disabled servers. Build it the way construction
		// does, so a preview can start the login.
		if m.oauthStore == nil {
			return nil, errors.New("OAuth storage is unavailable")
		}
		provider := newOAuthProvider(cfg, m.oauthStore, m.http)
		m.mu.Lock()
		m.oauth[name] = provider
		m.handlers[name] = provider
		m.mu.Unlock()
	}

	session, remote, state, diagnostic := m.connectServer(ctx, cfg)
	if session != nil {
		defer session.Close()
	}
	if state == StateLoginRequired {
		return nil, ErrLoginRequired
	}
	if state != StateReady {
		return nil, fmt.Errorf("MCP server %q: %s", name, diagnostic)
	}
	previews := make([]ToolPreview, 0, len(remote))
	for _, tool := range remote {
		if tool == nil {
			continue
		}
		previews = append(previews, ToolPreview{Name: tool.Name, Description: tool.Description})
	}
	return previews, nil
}
