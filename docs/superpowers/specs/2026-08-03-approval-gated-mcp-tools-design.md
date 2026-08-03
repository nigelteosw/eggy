# Approval-gated MCP tools

Closes R1 in `TODO.md`: the open safety gap left by deleting Calendar.

## The gap

A configured MCP server is trusted wholesale. Every tool it advertises executes
the moment the model calls it, so a mail send or a calendar mutation arriving
over MCP carries no per-call approval, while the same class of action through a
native tool would.

The approval machinery is not missing — it is unused. `Request`,
`DeliverApproval`, `turns.Approval`, `ApprovalService.Authorize` and
`ExecuteApproved` are all built and tested, and `approvalExecutors` in
`internal/bootstrap/app.go` is an empty map. Nothing has requested an approval
in production since Calendar was deleted. This is that machinery's first
consumer, which is why the design has to settle semantics no shipped code pins.

## Shape

A per-server `require_approval` list of remote tool names. A matching call is
not executed; it requests an approval bound to the exact arguments, and the
owner's tap runs it.

**The gate is kernel-side and provider-neutral.** `internal/kernel/services`
owns a decorator that wraps any `ports.Tool`. Putting it there rather than in
`plugins/tools/mcp` keeps `approvals` out of the adapter and means gating a
native tool later reuses this instead of growing a second implementation.

**Wrapping happens at the provider boundary.** `registry.AddProvider("mcp", …)`
already re-reads the catalog every turn; the wrap happens inside that function,
so a mid-session reconnect cannot drop the gate. The MCP manager's only new job
is to answer whether a flattened `server__tool` name is protected.

**One action, one executor.** Every gated call is issued under the single
action `tool.call`. `ExecuteApproved` calls `Authorize` first — payload-digest
bound, so approving one call never authorizes different arguments or a
different tool — then re-resolves the tool from the live registry by name and
executes the *inner* tool, bypassing the gate exactly once. Re-resolving rather
than capturing a pointer is what lets an approval survive a reconnect or a
restart.

## Post-approval semantics

The tool returns "awaiting the owner's approval" to the model and the turn
ends. On approval the executor performs the call and the result is delivered as
an outcome message; the model never sees it.

This is what `turns.Approval` already does, and it is adequate because
`require_approval` targets mutations, where the result is a confirmation rather
than input to more reasoning. Resuming the turn with the result would need a
new turn, conversation-thread plumbing, and a re-entrancy rule the loop does
not have. Not built.

The gated tool's description says so explicitly, so the model stops rather than
retrying the call in a loop.

## Auto mode

`/auto` switches every gate off, and `/auto` again switches it back on; the
panel's Approvals card carries the same toggle, and both report the resulting
state in one shared wording. It is a toggle rather than a setting on both
surfaces, so neither can ask for a state the other cannot express.

It is a real bypass, not a softened gate: a gated tool runs inline and returns
its result to the model exactly as an ungated one would, and no approval is
recorded. Three things keep that honest rather than hidden:

- it is durable (`state.ApprovalAutoMode`), because a bypass that quietly
  reset at restart would be worse than one that persists visibly;
- `/status` reports it whenever it is on, and says nothing when it is off —
  the failure worth naming is the switch someone flipped and forgot;
- an unreadable switch leaves the gate standing. A state store that cannot be
  read fails the call rather than being read as "off".

## Matching

Exact remote tool names, matching `tool_filter`'s existing behaviour. No globs:
a glob that silently matches nothing is a safety hole, and the failure is
invisible until the unapproved call has already run.

## Invariants pinned by tests

- A protected tool never reaches the server without an approval.
- An approval for tool A with arguments X cannot execute A with arguments Y,
  nor tool B at all.
- The gate survives a catalog rebuild — a reconnect does not un-gate a tool.
- An unprompted turn cannot request one. It already cannot reach MCP; that must
  stay true now that a tool call has a durable side effect.
- Unconfigured costs nothing: no wrapper, no executor, no changed prompt bytes.
- Auto mode runs the call and returns its own result; switching it back off
  restores the gate on the next call, without a restart.
- An unreadable auto-mode switch does not open the gate.
- The panel's toggle requires a session.

## Deletion budget

+2 config keys (`require_approval` per MCP server, `approval_auto_mode` in
state), +~200 production lines, +1 Telegram command, +2 web routes, 0 new
tools, 0 new ports, 0 new durable record types (reuses `state.Approvals`),
0 background loops.

Above the ~90 estimated at design time, because auto mode was added during
implementation: it is a second surface (chat and panel) over one switch, and
the switch has to be readable, writable, and reported by `/status`.
