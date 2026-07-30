package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/nigelteosw/eggy/internal/ports"
)

var ErrDuplicateTool = errors.New("duplicate tool")

// ToolRegistry is the single source of tools a turn runs on. It holds the
// tools registered once at startup, and optionally a set of live providers
// whose catalogs change while the process runs.
//
// It exists so the loop has exactly one place to ask. An adapter with a
// changing catalog -- an MCP manager that reconnects, reloads, or is logged
// out of -- is a provider here rather than a second source bolted onto the
// loop, which is what keeps that adapter from being a modification to core
// agent machinery.
type ToolRegistry struct {
	tools     map[string]ports.Tool
	providers []func() []ports.Tool
}

func NewToolRegistry() *ToolRegistry { return &ToolRegistry{tools: map[string]ports.Tool{}} }

func (r *ToolRegistry) Register(tool ports.Tool) error {
	name := tool.Definition().Name
	if name == "" {
		return errors.New("tool name is empty")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTool, name)
	}
	r.tools[name] = tool
	return nil
}

// AddProvider installs a live source of additional tools, read fresh on every
// Tools call so a catalog that changes takes effect on the next turn.
//
// A provider can never shadow a registered tool: a registered name wins every
// collision, which is what keeps the kernel primitives (read_file, terminal,
// read_file) defined exactly once no matter what an MCP server
// advertises. Unlike Register, this reports no error for a collision -- a
// remote catalog is not under our control, so one badly named remote tool
// drops out rather than failing the process.
func (r *ToolRegistry) AddProvider(provider func() []ports.Tool) {
	if provider == nil {
		return
	}
	r.providers = append(r.providers, provider)
}

// Tools is the merged catalog: every registered tool in name order, then
// each provider's tools in name order, skipping any name already taken.
func (r *ToolRegistry) Tools() []ports.Tool {
	result := sortedTools(r.tools)
	if len(r.providers) == 0 {
		return result
	}
	taken := make(map[string]bool, len(r.tools))
	for name := range r.tools {
		taken[name] = true
	}
	for _, provider := range r.providers {
		supplied := map[string]ports.Tool{}
		for _, tool := range provider() {
			name := tool.Definition().Name
			if name == "" || taken[name] {
				continue
			}
			taken[name] = true
			supplied[name] = tool
		}
		result = append(result, sortedTools(supplied)...)
	}
	return result
}

func sortedTools(tools map[string]ports.Tool) []ports.Tool {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ports.Tool, 0, len(names))
	for _, name := range names {
		result = append(result, tools[name])
	}
	return result
}

// DecodeToolInput is the strict JSON decode every kernel tool uses on its
// arguments: unknown fields and trailing JSON are errors, and an empty body
// means "{}". It is exported because tools live in more than one package now
// (see services/repo) and every one of them must reject a malformed call the
// same way.
func DecodeToolInput(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid tool input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid tool input: trailing JSON")
	}
	return nil
}
