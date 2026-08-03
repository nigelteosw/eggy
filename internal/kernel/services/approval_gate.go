package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ApprovalToolCall is the one action every approval-gated tool call is issued
// under. One action and one executor, per the standing safety rule: the
// approval binds the tool name and the arguments in its payload, so a single
// action never lets an approval for one call authorize another.
const ApprovalToolCall approvals.Action = "tool.call"

// approvalNotice is appended to a gated tool's description. Without it the
// model reads the "awaiting approval" result as a transient failure and calls
// the tool again, burning the turn on a request the owner has already been
// asked once.
const approvalNotice = " This tool requires the owner's approval: calling it asks them to approve, and the call runs only if they do. The result is delivered to the owner rather than returned here, so do not call it again while waiting."

// ApprovalRequester is the narrow view of ApprovalService a gate needs.
type ApprovalRequester interface {
	Request(ctx context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error)
}

// AutoApprover reports whether the owner has switched the gate off. It is read
// per call rather than captured at wiring time, so turning the bypass off
// takes effect on the very next tool call rather than at the next restart --
// the direction that matters, since the unsafe state is the one left on.
type AutoApprover interface {
	AutoApprove(ctx context.Context) (bool, error)
}

// ToolLookup finds a tool by name in the live catalog. The executor resolves
// through this at execution time rather than capturing the tool it gated,
// because an approval outlives the catalog it was issued against: an MCP
// reconnect replaces every tool object, and a restart rebuilds them all while
// the pending approval survives in state.
type ToolLookup interface {
	Lookup(name string) (ports.Tool, bool)
}

// gatedCall is an approval's payload: what would run if the owner approves.
// The tool name is the flattened catalog name, which for an MCP tool already
// carries the server it came from, so nothing else has to be recorded to know
// where an approved call will land.
type gatedCall struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// approvalGatedTool defers a tool call to the owner. It advertises the wrapped
// tool's schema unchanged -- the model calls it exactly as it otherwise
// would -- and turns the call into an approval request instead of executing
// it.
type approvalGatedTool struct {
	inner      ports.Tool
	requester  ApprovalRequester
	auto       AutoApprover
	definition ports.ToolDefinition
}

// NewApprovalGatedTool wraps a tool so calling it requests the owner's
// approval instead of running. A nil auto keeps the gate permanently on.
func NewApprovalGatedTool(inner ports.Tool, requester ApprovalRequester, auto AutoApprover) ports.Tool {
	definition := inner.Definition()
	definition.Description += approvalNotice
	return &approvalGatedTool{inner: inner, requester: requester, auto: auto, definition: definition}
}

func (t *approvalGatedTool) Definition() ports.ToolDefinition { return t.definition }

// Unwrap is how the executor reaches the real tool after an approval. It is
// the only way past the gate, and it is reachable only from a decided
// approval, so a gated tool cannot be executed by asking the registry for it.
func (t *approvalGatedTool) Unwrap() ports.Tool { return t.inner }

func (t *approvalGatedTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	// Auto mode runs the call inline and returns its real result, which is the
	// point of the bypass: the model keeps working instead of ending the turn.
	// A failure to read the switch is not treated as "off" -- an unreadable
	// state store must leave the gate standing, not open it.
	if t.auto != nil {
		auto, err := t.auto.AutoApprove(ctx)
		if err != nil {
			return nil, err
		}
		if auto {
			return t.inner.Execute(ctx, raw)
		}
	}
	call := gatedCall{Tool: t.definition.Name, Arguments: normalizeArguments(raw)}
	approval, err := t.requester.Request(ctx, ApprovalToolCall, call, approvalSummary(call))
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Status     string `json:"status"`
		ApprovalID string `json:"approval_id"`
		Detail     string `json:"detail"`
	}{
		Status: "awaiting_approval", ApprovalID: approval.ID,
		Detail: "The owner has been asked to approve this call. It has not run. Its result will be delivered to them, not returned here.",
	})
}

// ApprovalToolExecutor runs what an approve tap authorized. It is registered
// for ApprovalToolCall and is the only path that executes a gated tool.
type ApprovalToolExecutor struct {
	tools    ToolLookup
	policy   ports.ApprovalPolicy
	maxBytes int
}

// approvedResultLimit bounds what an approved call reports back to the owner.
// The result is rendered into a chat message, and an MCP tool can return far
// more than a message should carry.
const approvedResultLimit = 2000

func NewApprovalToolExecutor(tools ToolLookup, policy ports.ApprovalPolicy) *ApprovalToolExecutor {
	return &ApprovalToolExecutor{tools: tools, policy: policy, maxBytes: approvedResultLimit}
}

func (e *ApprovalToolExecutor) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	var call gatedCall
	if err := json.Unmarshal(approval.Payload, &call); err != nil {
		return nil, fmt.Errorf("decode approved tool call: %w", err)
	}
	if call.Tool == "" {
		return nil, errors.New("approved tool call names no tool")
	}
	// Authorize before anything else, and before the lookup: it is what binds
	// this execution to this approval's action, payload digest and expiry.
	// Re-encoding the decoded payload rather than trusting approval.Payload is
	// deliberate -- the digest is checked against what will actually run.
	call.Arguments = normalizeArguments(call.Arguments)
	if err := e.policy.Authorize(ctx, ApprovalToolCall, call, approval.ID); err != nil {
		return nil, err
	}
	tool, ok := e.tools.Lookup(call.Tool)
	if !ok {
		return nil, fmt.Errorf("approved tool %q is no longer available", call.Tool)
	}
	// The gate is unwrapped exactly once, here. Executing the wrapper would
	// request a second approval for a call the owner has already approved.
	if gated, wrapped := tool.(*approvalGatedTool); wrapped {
		tool = gated.Unwrap()
	}
	result, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return nil, err
	}
	return truncateResult(string(result), e.maxBytes), nil
}

func normalizeArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// approvalSummary is what the owner reads on the approve prompt. It names the
// tool and shows the arguments, because approving "call a tool" without seeing
// what it would do is not a decision.
func approvalSummary(call gatedCall) string {
	arguments := string(call.Arguments)
	if arguments == "{}" {
		return fmt.Sprintf("Call %s with no arguments", call.Tool)
	}
	return fmt.Sprintf("Call %s with %s", call.Tool, truncateResult(arguments, 500))
}

func truncateResult(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("… (%d bytes truncated)", len(value)-limit)
}
