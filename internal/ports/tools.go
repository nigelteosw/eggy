package ports

import (
	"context"
	"encoding/json"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	// Effect is what ModeNormal acts on. Its zero value says the tool changes
	// something, so a tool nobody classified is gated rather than trusted:
	// forgetting costs an approval prompt, and the opposite mistake costs the
	// mutation itself.
	Effect ToolEffect `json:"effect,omitzero"`
}

// ToolEffect classifies what calling a tool does.
//
// It is declared by the tool rather than looked up in a table somewhere else,
// because a table is a second place to forget: whoever adds an action is
// already editing the file the classification lives in.
//
// MCP tools are the deliberate exception and leave this alone. A remote
// catalog cannot be classified from here -- nothing in Eggy knows whether
// railway_deploy writes -- so an MCP server stays governed by the
// require_approval list on its own configuration, which is the trust decision
// the owner already made by configuring it.
type ToolEffect struct {
	// ReadOnly marks a tool that changes nothing at all, and is one of the two
	// claims that let a call through in ModeNormal.
	ReadOnly bool `json:"read_only,omitempty"`
	// Internal marks a tool whose writes land only in Eggy's own owner-visible
	// context documents and nowhere else -- no message sent, no calendar
	// changed, no job left running after the turn.
	//
	// It is the second claim ModeNormal honors, and it exists for exactly one
	// thing: curating USER.md and MEMORY.md. Remembering a fact is not a
	// decision an owner wants put to them; asking costs a prompt per remembered
	// fact, which is the training-to-tap-approve failure the gate exists to
	// avoid, and the result is a line in a file the owner can already read and
	// correct. ModeStrict still gates it, so the owner who wants to see every
	// call still does.
	//
	// Nothing that reaches outside Eggy may claim this, whatever the blast
	// radius: "small" is not the test, "nobody but the owner can observe it" is.
	Internal bool `json:"internal,omitempty"`
	// Mutations names the actions that write, for a tool carrying several
	// operations behind one schema. Empty on a tool that is not ReadOnly means
	// every call to it writes.
	Mutations []string `json:"mutations,omitempty"`
}

// GateAllTool is the action name that means "every call to this tool writes".
// Spelling it out is clearer at a call site than an empty Mutations, which
// reads like nobody filled it in -- and is what the zero value already means.
const GateAllTool = "*"

// ReadOnlyTool is the classification for a tool that only reads.
func ReadOnlyTool() ToolEffect { return ToolEffect{ReadOnly: true} }

// InternalTool is the classification for a tool that writes only Eggy's own
// owner-visible context documents. See ToolEffect.Internal for why it is not
// gated in ModeNormal, and why nothing that reaches outside Eggy may use it.
func InternalTool() ToolEffect { return ToolEffect{Internal: true} }

// MutatingActions classifies a tool whose named actions write and whose others
// do not.
func MutatingActions(actions ...string) ToolEffect { return ToolEffect{Mutations: actions} }

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

type ApprovalPolicy interface {
	Authorize(context.Context, approvals.Action, any, string) error
}
