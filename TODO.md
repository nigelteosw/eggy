# Eggy roadmap

This checklist captures the practical lessons Eggy can adopt from OpenClaw,
Hermes Agent, and NanoClaw. It preserves Eggy's Go ports-and-adapters
architecture, file-backed state, trusted-repository model, and independent
approval gates.

## P0: Keep read-only repository work narrow and safe

- [x] Stop `repository_inspect` from launching a modifying implementation run.
- [ ] Replace it with narrow provider-neutral repository read capabilities:
  - [x] list configured repositories;
  - [x] list a bounded directory tree;
  - [x] search file names and text;
  - [x] read bounded file ranges;
  - [x] inspect status and branches without mutation;
  - [ ] inspect diffs without mutation (not meaningful yet: read tools clone a
        fresh checkout per call, so there is never a pending diff to show);
  - [x] read safe GitHub repository, issue, pull-request, and check metadata.
- [x] Keep clone credentials and other secrets outside model-visible tool
      arguments and results.
- [x] Enforce repository roots, path validation, output bounds, timeouts, and
      sanitized environments for every read tool.
- [x] Ensure read-only repository work creates no branch, leaves no diff, and
      cannot invoke dependency installation or arbitrary shell commands.
- [ ] Require successful read-tool evidence before Eggy claims facts about a
      repository's implementation. (tool descriptions hint at this; nothing
      enforces it yet.)
- [x] Return a truthful capability/setup response when repository reading is not
      configured or available.

## P1: Preserve host authority over coding and shipping

- [ ] Keep workspace creation, repository cloning, branch creation, diff capture,
      and cleanup under Eggy rather than the coding-agent adapter.
- [ ] Treat coding-agent output as an untrusted proposal until Eggy independently
      captures and validates the resulting checkout state.
- [ ] Record the base commit before execution and the final commit/diff state
      afterward.
- [ ] Reject changed, incomplete, or truncated approval material.
- [x] Preserve separate, expiring, payload-bound approval records for commit,
      push, and pull-request creation, even though `ShippingService.Ship`
      decides each one automatically instead of waiting for an owner tap —
      payload-digest binding, expiry, and protected-branch enforcement all
      still run unchanged inside `ApprovalService.Authorize`.
- [ ] Revalidate local and remote heads immediately before protected side
      effects.
- [ ] Keep protected branches unpushable even with approval.
- [ ] Never let the implementation loop merge a pull request.
- [ ] Represent privileged requests and results as structured kernel data rather
      than natural-language messages.
- [ ] Add tests proving the implementation loop cannot bypass Eggy's shipping
      policy.

## P2: Improve memory without turning it into a transcript dump

Eggy's `SOUL.md`/`USER.md`/`MEMORY.md` naming and layering is adopted from
Hermes Agent, but the storage model deliberately diverges rather than catching
up to it:

- Hermes persists sessions and full message history in SQLite
  (`~/.hermes/state.db`, WAL mode) with FTS5 full-text search and a
  `session_search` tool for cross-session recall. Eggy keeps only a bounded
  recent-history window plus a summary in `state.json`, with no full-text
  search over past conversations and no database dependency.
- Hermes curates memory proactively ("periodic nudges") and grows a separate
  procedural-skill store under `~/.hermes/skills/`. Eggy curates `USER.md` and
  `MEMORY.md` only through explicit, narrow agent tool calls (`user_append`,
  `memory_append`, `user_replace_section`, `memory_replace_section`) and has no
  autonomous skill-creation loop (see "Separate durable facts from reusable
  procedures" below for Eggy's intentionally smaller take on that idea).
- Hermes layers in dialectic user modeling (Honcho) for a deepening
  cross-session user model. Eggy keeps `USER.md` a flat, agent-curated fact
  list.
- Eggy rejects likely secrets (passwords, tokens, keys) before any memory
  write reaches disk; this guard was not confirmed in Hermes's public docs.

The gap is intentional: Eggy stays file-backed, single-volume, and
provider-neutral rather than adding a database, search index, or skills
runtime purely to match Hermes.

- [ ] Set explicit injected-size budgets for `USER.md` and `MEMORY.md`.
- [ ] Keep `USER.md` for stable owner preferences and communication style.
- [ ] Keep `MEMORY.md` for compact durable facts, decisions, and reusable lessons.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and invisible
      Unicode content before memory writes.
- [ ] Return a clear capacity error when curated memory is full instead of
      silently truncating or dropping stored content.
- [ ] Let the agent consolidate or remove stale entries through the existing
      controlled, atomic memory tools.
- [ ] Design bounded, file-backed conversation search before adding it; do not
      introduce a database solely for transcript recall.
- [ ] Keep recalled conversation excerpts bounded, redacted, and marked as stale
      historical context rather than current authority.

## P2: Separate durable facts from reusable procedures

- [ ] Define a lightweight, Markdown-based procedural-skill format without adding
      an agent framework, plugin runtime, or arbitrary native extension system.
- [ ] Load full skill instructions only when the current task matches; keep only
      compact skill metadata in ordinary context.
- [ ] Store repeated workflows and troubleshooting procedures in skills rather
      than expanding `MEMORY.md`.
- [ ] Let Eggy propose a skill after a successful complex workflow, a recovered
      failure, or an owner correction.
- [ ] Require explicit owner approval before Eggy creates, edits, or deletes a
      procedural skill.
- [ ] Validate skill names, paths, size, referenced files, and forbidden secret
      content.
- [ ] Keep installed skills inspectable and removable through deterministic
      commands.

## P2: Make context and capabilities inspectable

- [ ] Add a deterministic `/capabilities` view showing:
  - selected reasoning model;
  - registered assistant tools;
  - configured repositories;
  - enabled integrations;
  - implementation-loop readiness.
- [ ] Add a deterministic `/context` view showing:
  - injected bytes or estimated tokens per context file;
  - conversation summary and recent-history sizes;
  - tool-definition/schema overhead;
  - truncation or omitted-context markers;
  - the current context limit and remaining budget when known.
- [ ] Add safe `/runs` detail for coding-agent name, base revision, current phase,
      provider session ID, elapsed time, and validation status.
- [ ] Report loaded/missing capabilities from actual bootstrap state rather than
      model assumptions.
- [ ] Never expose credentials, raw environment contents, or credential paths in
      these diagnostics.

## P2: Tighten heartbeat and scheduled work

- [ ] Give heartbeat turns a small heartbeat-specific context instead of the full
      conversational history by default.
- [ ] Evaluate a human-editable `HEARTBEAT.md` checklist while keeping timing,
      quiet hours, limits, and prohibited actions in deterministic policy.
- [ ] Run heartbeat evaluation in an isolated conversation context so old chat
      instructions are not accidentally revived.
- [ ] Skip or defer heartbeats while an implementation run or another protected
      workflow is busy.
- [ ] Keep active-hour and timezone checks deterministic.
- [ ] Ensure "nothing useful to report" produces no Telegram message.
- [ ] Distinguish deterministic scheduled commands from scheduled agent turns.
- [ ] Run deterministic reminders, watchdogs, and already-rendered notifications
      without spending a model call.
- [ ] Require scheduled agent prompts to be self-contained and start them without
      ambient chat history.

## P3: Improve execution isolation and recovery

- [ ] Preserve strict workspace roots, path validation, environment allowlists,
      timeouts, output limits, process-group cancellation, and cleanup.
- [ ] Evaluate container-per-implementation-run isolation as a future hardening
      step while keeping the current trusted-repository assumption explicit.
- [ ] If containers are added, use a non-root user, explicit mounts, dropped
      capabilities, bounded resources, and an explicit network policy.
- [ ] Keep credentials outside coding workspaces and forward only the minimum
      required environment to each subprocess.
- [ ] Persist enough run state to mark interrupted work accurately after restart.
- [ ] Keep resumable coding-agent sessions distinct from automatic replay: never
      resume an interrupted modifying run without a new owner instruction.
- [ ] When the owner explicitly asks to continue an already-open pull request,
      check out that pull request's existing branch as the run's base instead
      of branching from trunk and opening a second pull request.
- [ ] Track the open pull request associated with a coding-agent run so a later
      "keep going on that" request can be matched back to it instead of only
      matching on repository and instruction text.
- [ ] Save a bounded patch/diff artifact before workspace cleanup so rejected or
      interrupted work can be inspected without retaining the entire checkout.
- [ ] Add cleanup and retention diagnostics for abandoned workspaces and provider
      sessions.

## P3: Add checkpoints and rollback-friendly artifacts

- [ ] Treat the immutable base commit as the pre-implementation checkpoint.
- [ ] Capture a complete post-run diff and validation report before requesting
      approval.
- [ ] Let the owner discard an implementation run without affecting the source
      repository.
- [ ] Consider an explicit "retry from base" operation rather than continuing a
      contaminated or partially failed workspace.
- [ ] Keep rollback local to the isolated branch/workspace; never implement
      rollback with destructive operations against the owner's checkout.

## P3: Keep the extension model small

- [ ] Retain bootstrap-only adapter registration and provider-neutral kernel/port
      boundaries.
- [ ] Avoid copying OpenClaw's broad plugin surface or Hermes's large built-in
      tool registry into Eggy.
- [ ] Prefer a small capability manifest and focused tools over exposing every
      integration to every turn.
- [ ] Keep channel-specific formatting, provider payloads, credentials, and CLI
      protocols inside adapters.
- [ ] Add new providers through compiled adapters and explicit configuration, not
      runtime-loaded native plugins.

## Acceptance checklist

- [ ] The coding agent always runs in an isolated workspace, never directly
      against the owner's checkout.
- [ ] Ambiguous requests pause for clarification before any modifying workflow.
- [ ] Coding-agent progress is streamed through normalized, provider-neutral
      events.
- [ ] Eggy independently captures the final diff and validation evidence.
- [ ] Commit, push, and pull-request creation retain payload-bound approval
      records even though they're decided automatically; Calendar mutations
      still require an explicit owner tap.
- [ ] Context, memory, skills, and capability diagnostics remain bounded and
      secret-free.
- [ ] Existing `/data/state.json` files remain compatible or receive an explicit,
      tested schema migration.
- [ ] `make fmt vet test race build` passes.
- [ ] `make smoke` passes when Docker is available.
