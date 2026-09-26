package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nigelteosw/eggy/internal/ports"
)

type remoteTool struct {
	definition ports.ToolDefinition
	remoteName string
	// session is resolved per call rather than captured, so a tool built
	// before a reconnect keeps working over the session that replaced it.
	session   func() clientSession
	timeout   time.Duration
	maxBytes  int64
	before    func(context.Context) error
	onResult  func(error)
	executeMu *sync.Mutex
}

func newRemoteTool(server string, remote *sdk.Tool, session func() clientSession, timeout time.Duration, maxBytes int64) (*remoteTool, error) {
	if remote == nil {
		return nil, errors.New("MCP tool definition is nil")
	}
	name, err := normalizeToolName(server, remote.Name)
	if err != nil {
		return nil, err
	}
	schema, err := json.Marshal(remote.InputSchema)
	if err != nil {
		return nil, err
	}
	return &remoteTool{
		definition: ports.ToolDefinition{Name: name, Description: remote.Description, Schema: schema},
		remoteName: remote.Name, session: session, timeout: timeout, maxBytes: maxBytes,
	}, nil
}

func (t *remoteTool) Definition() ports.ToolDefinition { return t.definition }

func (t *remoteTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.executeMu != nil {
		t.executeMu.Lock()
		defer t.executeMu.Unlock()
	}
	if t.before != nil {
		if err := t.before(ctx); err != nil {
			return nil, err
		}
	}
	session := t.session()
	if session == nil {
		return nil, fmt.Errorf("MCP tool %q has no connected server", t.definition.Name)
	}
	var arguments map[string]any
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &arguments); err != nil || arguments == nil {
		return nil, errors.New("MCP tool arguments must be a JSON object")
	}
	callCtx := ctx
	cancel := func() {}
	if t.timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, t.timeout)
	}
	defer cancel()
	result, err := session.CallTool(callCtx, &sdk.CallToolParams{Name: t.remoteName, Arguments: arguments})
	if err == nil {
		var converted json.RawMessage
		converted, err = convertResult(result, t.maxBytes)
		if t.onResult != nil {
			t.onResult(err)
		}
		return converted, err
	}
	if t.onResult != nil {
		t.onResult(err)
	}
	return nil, err
}

var _ ports.Tool = (*remoteTool)(nil)
