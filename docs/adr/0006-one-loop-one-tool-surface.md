# ADR 0006: One loop and one tool surface, and a narrowed unprompted-turn invariant

## Context

ADR 0002 removed the Codex/Claude Code CLI subprocess and made the selected
reasoning model the only execution engine. It kept the subprocess's *shape*:
edits still run inside a second, bounded `agent.Loop` with its own tool
registry, driven synchronously by a blocking `repository_modify` tool call in
the outer conversational loop.

That residue costs more than it saves. `read_file` and `terminal` are defined
twice with different schemas and different workspace resolution — the
conversational pair clones an ephemeral checkout and destroys it after every
call, so conversational repository exploration is stateless and structurally
cannot accumulate understanding of a repo. The two loops have incompatible
termination conditions (no tool calls, versus `finish_implementation`), which
cannot be reconciled while shipping is a run *outcome* rather than an action.
And compaction, durable transcripts, and progress streaming exist only for
implementation sessions, leaving the conversational loop capped at a fixed
step budget with no checkpoint.

Separately, the invariant "scheduled and heartbeat turns cannot reach
repository write tools" is enforced by tool-registry allowlists in
`internal/bootstrap`. It was always a proxy for the invariant that matters:
nothing lands without a payload-bound authorization and a human-reviewed pull
request.

## Decision

One loop, one kernel-owned primitive tool set (`read_file`, `terminal`,
`patch`, `write_file`) resolving its workspace from session state, and one
durable transcript per thread. "Coding" stops being a mode and becomes what
happens when the session has a workspace attached and the write primitives
aren't gated. Writes are gated by *result* — `patch` and `write_file` stay
registered and return an explicit error on a read-only workspace — rather
than by presence in a registry, so the model's tool list does not change
shape depending on which engine is running. Steering is prompt plus per-turn
permission, never a second engine; adapters and MCP servers extend *around*
the primitive set with namespaced tools and never redefine a primitive.

The workspace moves off the run and onto the conversation thread, cloned once
by `workspace_open`, so inspect → edit → discuss is one continuous transcript.
Shipping becomes a non-terminal `propose_change` that returns the pull-request
URL as a tool result and lets the loop continue.

Scheduled and heartbeat turns gain a `propose_improvement` path — isolated
branch, draft pull request, never a base branch — and the registry allowlists
are reworked around the narrowed invariant: an unprompted turn cannot target
a base branch, cannot open a non-draft pull request, and still cannot reach
MCP tools or perform arbitrary repository writes.

## Consequences

This is the continuation of ADR 0002, not a reversal of it. ADR 0002's stated
consequence was "one engine, one context model"; it delivered the first and
deferred the second. Collapsing the loops finishes that sentence — compaction,
durable transcripts, and progress streaming become properties of every turn
rather than of coding runs, and the conversational agent gains persistent
repository understanding instead of inspecting and suggesting.

Every safety invariant from ADR 0002 and ADR 0003 survives unchanged:
payload-digest approvals, protected-branch denial, HEAD revalidation before
push and PR, never merging, and Eggy's own model-independent diff and
validation capture. None of them required two loops; they are properties of
`ShippingService`.

The narrowing of the unprompted-turn rule is deliberate, and it trades a
coarse guarantee that is easy to state for a precise one that must be tested.
Its enforcement therefore moves out of `internal/bootstrap` — where no kernel
test can guard it — into the kernel `TurnService`, and the assertions above
become tests rather than allowlist entries. Until that move lands, the
narrowing is not safe to ship.
