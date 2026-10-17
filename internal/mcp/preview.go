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

// ErrStdioNotPreviewed means the server runs a local program. A review must not
// start a process, so stdio servers are enabled from config.yaml, not previewed.
var ErrStdioNotPreviewed = errors.New("stdio servers are not previewed: they run a local program")

// Preview connects to a configured server once and lists the tools it
// advertises, whether or not the server is enabled. Nothing is added to the
// running catalog and the server's status is left as it was. A bearer handler
// for a disabled server is built for this call only. An OAuth provider is
// recorded on the manager when the server has none, because the login the review
// may ask for needs it; an existing provider is never replaced.
func (m *Manager) Preview(ctx context.Context, name string) ([]ToolPreview, error) {
	m.mu.RLock()
	runtime, ok := m.runtimes[name]
	handler := m.handlers[name]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrRestartRequired
	}
	cfg := runtime.config
	if cfg.Transport == TransportStdio {
		return nil, ErrStdioNotPreviewed
	}
	if handler == nil {
		switch cfg.Auth {
		case "oauth":
			// Construction skips disabled servers, so a server disabled at boot
			// has no provider. Build one the way construction does.
			if m.oauthStore == nil {
				return nil, errors.New("OAuth storage is unavailable")
			}
			m.mu.Lock()
			if existing := m.handlers[name]; existing != nil {
				handler = existing
			} else {
				provider := newOAuthProvider(cfg, m.oauthStore, m.http)
				m.oauth[name] = provider
				m.handlers[name] = provider
				handler = provider
			}
			m.mu.Unlock()
		case "bearer-env":
			handler = newBearerHandler(cfg.BearerToken)
		}
	}

	session, remote, state, diagnostic := m.connectServerWith(ctx, cfg, handler, false)
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
