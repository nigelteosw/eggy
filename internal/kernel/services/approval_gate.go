package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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

// GateAllCalls in a ToolEffect's Mutations, or an empty Mutations on a tool
// that is not read-only, means every call writes.
const GateAllCalls = "*"

// RuleFor turns a tool's own classification into the rule ModeNormal applies.
//
// This is the join between the two halves of the mechanism: a tool says what
// its calls do, and this decides what that means for the gate. Keeping the
// translation in one function is what stops "which calls write" and "which
// calls are gated" from drifting into two different answers.
func RuleFor(definition ports.ToolDefinition) ApprovalRule {
	effect := definition.Effect
	if effect.ReadOnly || effect.Internal {
		// Gated by nothing in normal mode, and still gated by strict, which is
		// the whole reason such a tool is wrapped at all. An internal write
		// joins a read here rather than getting a branch of its own: normal
		// mode asks about what leaves Eggy, and neither one does.
		return ApprovalRule{Gated: func(json.RawMessage) bool { return false }}
	}
	if len(effect.Mutations) == 0 || slices.Contains(effect.Mutations, GateAllCalls) {
		return ApprovalRule{}
	}
	gated := make(map[string]bool, len(effect.Mutations))
	for _, action := range effect.Mutations {
		gated[action] = true
	}
	listed := slices.Clone(effect.Mutations)
	slices.Sort(listed)
	return ApprovalRule{
		Gated: func(arguments json.RawMessage) bool {
			var call struct {
				Action string `json:"action"`
			}
			// An unreadable or actionless payload is gated. The two mistakes
			// do not cost the same: an unnecessary prompt is an annoyance, an
			// ungated mutation is what the gate exists to stop.
			if err := json.Unmarshal(arguments, &call); err != nil || call.Action == "" {
				return true
			}
			return gated[call.Action]
		},
		Notice: fmt.Sprintf(" These actions require the owner's approval: %s. Calling one asks them, and it runs only if they approve; the result then goes to the owner rather than back here, so do not call it again while waiting. Every other action on this tool runs normally.", strings.Join(listed, ", ")),
	}
}

// ApprovalRule narrows a gate to some of a tool's calls.
//
// A tool that carries several operations behind one schema -- the Google
// products, where reading mail and sending it are two actions on one tool -- is
// the case this exists for. Gating such a tool whole would put an approval
// prompt in front of reading the calendar, which trains the owner to approve
// without looking, and that is worse than no gate at all.
//
// Gated is asked per call, against the same arguments the approval would carry.
// Notice replaces the description suffix, because a model told "this tool
// requires approval" when only two of its actions do will avoid the other five.
// The zero value gates every call with the standard notice.
type ApprovalRule struct {
	Gated  func(arguments json.RawMessage) bool
	Notice string
}

// ApprovalRequester is the narrow view of ApprovalService a gate needs.
type ApprovalRequester interface {
	Request(ctx context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error)
}

// ModeReader reports how much the owner is currently being asked. It is read
// per call rather than captured at wiring time, so a mode change takes effect
// on the very next tool call rather than at the next restart -- the direction
// that matters, since the permissive state is the one left on.
type ModeReader interface {
	Mode(ctx context.Context) (ports.ApprovalMode, error)
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
	modes      ModeReader
	gated      func(json.RawMessage) bool
	definition ports.ToolDefinition
}

// NewApprovalGatedTool wraps a tool so calling it requests the owner's
// approval instead of running. A nil modes reader keeps the gate permanently
// on, whatever the owner has selected.
func NewApprovalGatedTool(inner ports.Tool, requester ApprovalRequester, modes ModeReader) ports.Tool {
	return NewApprovalGatedToolIf(inner, requester, modes, ApprovalRule{})
}

// NewApprovalGatedToolIf is the same gate under a rule. It is one mechanism
// with one wrapper type and one action, not a second approval path: an
// ungated call runs inline exactly as it would unwrapped, and a gated one
// takes the identical route through ApprovalToolCall.
//
// The rule describes what ModeNormal gates. ModeStrict gates the call whatever
// the rule says and ModeAuto gates nothing, so both are decided here rather
// than by wrapping different tools at startup -- the mode is durable runtime
// state and changes without a restart, so which tools carry a gate cannot
// depend on what it was at boot.
func NewApprovalGatedToolIf(inner ports.Tool, requester ApprovalRequester, modes ModeReader, rule ApprovalRule) ports.Tool {
	definition := inner.Definition()
	// A tool nothing gates in normal mode carries no notice: it is only
	// reachable through strict mode, where the awaiting-approval result says
	// the same thing at the moment it becomes true, and where a notice on
	// every read-only tool would be prompt bytes every owner pays for a mode
	// almost none of them run.
	if notice := rule.Notice; notice != "" {
		definition.Description += notice
	} else if rule.Gated == nil {
		definition.Description += approvalNotice
	}
	return &approvalGatedTool{inner: inner, requester: requester, modes: modes, gated: rule.Gated, definition: definition}
}

func (t *approvalGatedTool) Definition() ports.ToolDefinition { return t.definition }

// Unwrap is how the executor reaches the real tool after an approval. It is
// the only way past the gate, and it is reachable only from a decided
// approval, so a gated tool cannot be executed by asking the registry for it.
func (t *approvalGatedTool) Unwrap() ports.Tool { return t.inner }

func (t *approvalGatedTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	// A nil reader is the permanently-gated case and skips the lookup, so a
	// caller that wants an unconditional gate gets one without depending on
	// the state store being readable.
	mode := ports.ModeStrict
	if t.modes != nil {
		read, err := t.modes.Mode(ctx)
		if err != nil {
			// Not treated as auto: an unreadable state store must leave the
			// gate standing, not open it.
			return nil, err
		}
		mode = read
	}
	switch mode {
	case ports.ModeAuto:
		// Auto runs the call inline and returns its real result, which is the
		// point of the bypass: the model keeps working instead of ending the
		// turn.
		return t.inner.Execute(ctx, raw)
	case ports.ModeNormal:
		// A call the rule does not cover was never a decision the owner
		// deferred -- reading a calendar is not -- so it runs inline.
		if t.gated != nil && !t.gated(normalizeArguments(raw)) {
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
	return boundApprovedResult(result, e.maxBytes), nil
}

// boundApprovedResult holds an approved call's result inside the message bound
// without losing the part of it the owner actually reads.
//
// A result that carries a "summary" is saying "this line is the answer"; the
// record around it can be far longer than a chat message should be, and a
// schedule's is, because it embeds the instruction the owner just dictated.
// Truncating from the front would cut the summary off mid-JSON and leave the
// outcome message as an unparseable fragment of the owner's own words -- worst
// exactly where the summary matters most.
func boundApprovedResult(result json.RawMessage, maxBytes int) string {
	if maxBytes <= 0 || len(result) <= maxBytes {
		return string(result)
	}
	var payload struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(result, &payload); err == nil && strings.TrimSpace(payload.Summary) != "" {
		if compact, err := json.Marshal(payload); err == nil {
			return truncateResult(string(compact), maxBytes)
		}
	}
	return truncateResult(string(result), maxBytes)
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
