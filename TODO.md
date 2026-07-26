# Eggy roadmap

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; durable design rationale belongs in `docs/adr/`;
completed implementation history remains in git.

Priorities are ordered by urgency, then by dependency. Check an item only when
its implementation and focused tests have landed.

## P0: One loop, one tool surface

ADR 0002 removed the Codex/Claude Code CLI subprocess but kept its *shape*.
Eggy still runs two `agent.Loop` instances with two disjoint tool registries —
the conversational loop (`internal/bootstrap/app.go:377`) and the
implementation loop (`app.go:295`) — and `repository_modify`
(`internal/kernel/services/repository_tools.go:82`) is a blocking outer-loop
tool call that synchronously drives the inner loop to completion
(`services/coding.go:89`). Three consequences follow:

- `read_file` and `terminal` are defined **twice**, with different schemas and
  different workspace resolution: `repository_read_tools.go:16` takes a
  `repository` argument and clones an ephemeral checkout it destroys after
  *every single call*; `implementation_tools.go:16` resolves the workspace from
  ctx. Conversational repository exploration is therefore stateless and pays a
  full clone per `grep`, which is why the conversational agent can only inspect
  and suggest — it structurally cannot accumulate understanding of a repo.
- The two loops have incompatible termination conditions: `RunSelected`
  (`agent/loop.go:70`) ends when the model emits no tool calls;
  `RunImplementationWithEvents` (`loop.go:154`) ends when `finish_implementation`
  is called. These cannot be reconciled while shipping is a *run outcome*
  rather than an action.
- Compaction, durable transcripts, and progress streaming exist only for
  implementation sessions, so the conversational loop is capped at a hard
  40-step budget with no checkpoint.

The target: one loop, one kernel-owned primitive tool set, one durable
transcript per thread. "Coding" stops being a mode and becomes what happens
when the session has a workspace attached and the write primitives aren't
gated. Steering is prompt plus per-turn permission, never a second engine.
Adapters and MCP servers extend *around* the primitive set with namespaced
tools; they never redefine a primitive.

Every safety invariant survives unchanged — payload-digest approvals,
protected-branch denial, HEAD revalidation before push and PR, never merging.
None of them required two loops; they are properties of `ShippingService`.

- [ ] Write ADR 0006 recording this as the continuation of ADR 0002 (one
      engine, one context model) rather than a reversal of it, and recording
      the deliberate narrowing of the scheduled/heartbeat repository-write
      invariant (see "Let scheduled and heartbeat turns propose changes").

### Unify the tool surface

- [ ] Define one kernel-owned primitive tool set (`read_file`, `terminal`,
      `patch`, `write_file`) that resolves its workspace from session state
      instead of from a `repository` argument or a run-scoped ctx value.
- [ ] Gate writes by *result*, not by registry membership: `patch` and
      `write_file` stay registered and return an explicit error when the
      session's workspace is read-only, rather than being absent from the
      model's tool list depending on which loop is running.
- [ ] Delete `NewImplementationTools` and the duplicate `read_file`/`terminal`
      in `NewRepositoryReadTools`, keeping `repository_list` and
      `repository_github` as ordinary non-primitive tools.
- [ ] Cover it: one tool definition per primitive name across the whole
      registry, asserted as a test so a future adapter cannot reintroduce a
      shadowing primitive.

### Attach the workspace to the session, not the run

- [ ] Add `workspace_open(repository)` — clones once onto the durable volume
      under `runner.root` and records the checkout on the session — and
      `workspace_close`, which destroys it.
- [ ] Move the workspace off `ports.ImplementationSession` as a run property
      and onto the conversation thread, so inspect → edit → discuss is one
      continuous transcript with no lane transition and no re-clone.
- [ ] Retire the ephemeral clone-per-call path in `repository_read_tools.go`
      and the `withWorkspace`/`workspaceFromContext` ctx smuggling.
- [ ] Keep workspace cleanup bounded: extend `CleanupExpired` to reap
      thread-attached workspaces whose thread has gone idle past a cutoff.

This step is worth landing on its own even if the rest slips — it is mostly
deletion, and it is what turns "inspects and suggests" into "knows the repo."

### Hoist compaction and streaming into the one loop

- [ ] Move the checkpoint/compaction logic and the append-only event
      transcript out of `ImplementationSessions` (`/data/sessions/<id>/`) into
      `agent.Loop`, so every turn gets a durable transcript and a compaction
      checkpoint rather than only a coding run.
- [ ] Replace the fixed 40/48-step budgets with a context-budget checkpoint:
      the step limit stops meaning "how much work fits in a run" and starts
      meaning "when to compact."
- [ ] Keep the semantic-milestone rendering (`Plan:`, `Edited:`,
      `Validation:`) and `ports.ProgressReporter` destination routing exactly
      as they are; they are the streaming surface the unified loop reports on.

### Make shipping an action, not a run outcome

- [ ] Replace the terminal `finish_implementation` with a non-terminal
      `propose_change(summary, validation, commit_message)` that fires the
      existing commit → push → pull-request chain and **returns the
      pull-request URL as a tool result**. The loop continues afterwards, so
      the model reports the URL conversationally and can open a second pull
      request later in the same thread.
- [ ] Delete `NativeImplementer`, `Implementer`,
      `RunImplementationWithEvents`, `RunImplementation`,
      `implementationSystemPrompt` (`services/implementer.go:15`), and
      `modifyingRunnerContract` (`services/coding.go:246`); fold what remains
      of `CodingService` into workspace lifecycle plus session bookkeeping.
- [ ] Delete `repository_modify` and `repository_continue`. "Continue" becomes
      ordinary conversation against a thread whose workspace is still open.
- [ ] Keep the diff and validation capture Eggy performs *independently* of
      the model before shipping, and keep the pre-ship branch/HEAD equality
      checks that today live in `CodingService.Start`/`Resume`.
- [ ] Update the `hardRuntimePolicy` repository paragraph
      (`agent/prompt.go:49`) — it is written entirely around
      `repository_modify`/`repository_continue` and the run/approval framing
      that this step removes.

### Make turns asynchronous and steerable

- [ ] Run a turn in the background rather than blocking the inbound event, so
      a long editing turn does not hold the Telegram or web request open.
- [ ] Let an owner message that arrives during an active turn append to that
      turn's message list at the next step boundary, instead of starting a
      competing turn or queueing behind `Dispatcher.lockEvent`
      (`services/dispatcher.go:76`). This is the steering behavior — it is a
      dispatcher change, not a model change.
- [ ] Keep interruption working: `/stop` cancels the turn's context, and the
      cancellation milestone is still delivered on the turn's own destination
      (the existing "report on ctx, not runContext" property).
- [ ] Cover it: a message delivered mid-turn changes the turn's subsequent
      tool calls, and a `/stop` mid-turn leaves the workspace inspectable.

### Close the loop with pull-request checks

- [ ] Add a checks-completed event that resumes the still-open thread
      workspace for the pull request whose checks failed. Without this,
      self-improvement is one-shot; with it, it is a loop.
- [ ] Reuse `repository_github`'s `checks` read path for the evidence rather
      than adding a second GitHub surface.
- [ ] Depends on "Track the open pull request associated with each run"
      under P2: Improve run recovery and rollback.

### Let scheduled and heartbeat turns propose changes

The invariant "scheduled and heartbeat turns cannot reach repository write
tools" was always a proxy for the one that matters: *nothing lands without a
payload-bound authorization and a human-reviewed pull request*. Narrow it
deliberately rather than keeping the proxy.

- [ ] Add `self_repository` to `agent.CapabilityManifest`
      (`agent/prompt.go:12`) so the agent knows which registered repository is
      its own body, and that `AGENTS.md`, `docs/ARCHITECTURE.md`, and
      `docs/adr/` describe it.
- [ ] Give heartbeat and scheduled turns a `propose_improvement` path:
      isolated branch, draft pull request, never a base branch. Arbitrary
      repository writes from an unprompted turn stay barred.
- [ ] Rework the `readOnlyRunOptions`/`heartbeatRunOptions` allowlists
      (`internal/bootstrap/app_events.go:623-646`) around the narrowed
      invariant, and land it as a kernel test once the turn orchestrator moves
      (see "Split bootstrap into a core and its surfaces"): an unprompted turn
      cannot target a base branch, cannot open a non-draft pull request, and
      still cannot reach MCP tools.
- [ ] Update `docs/ARCHITECTURE.md`'s safety-invariant list and the standing
      constraint at the bottom of this file together with the code, not after.

## P0: Finish the architecture simplification

### Deployment follow-up

- [ ] Reset Railway's deployed `/data/config.yaml` so the next boot generates
      the current unversioned config shape. This is a manual deployment step.

## P0: Separate the agent core from its control surfaces

Telegram and the web UI are meant to be independent, equal channels into one
agent core, but the seam between them is still Telegram-shaped. The delivery
port itself is now clean — `ports.Channel` carries no chat identifier, each
channel resolves the turn's destination itself, and acking a Telegram button
tap has moved into that adapter's webhook handler. What remains is on the
receiving side: `config.Telegram.OwnerID` is still the universal owner
identity even for web-only events, and `events.Message`'s `ChatID` still means
"Telegram chat" or "web thread" depending on a string comparison against
`event.Source` (`app_events.go`'s `destinationFromEvent`).

Above that, `internal/bootstrap` is 10.5k lines holding four layers at once:
the composition root (`app.go`), the turn orchestrator (`app_events.go`), the
command surface (`commands*.go`, `command_catalog.go`, `command_result.go`),
and the HTTP surface (`web.go`, `chat.go`, `server.go`). The turn orchestrator
is core agentic behavior, not wiring — including the
`readOnlyRunOptions`/`heartbeatRunOptions` allowlists (`app_events.go:623-646`)
that encode a documented safety invariant in the one package no kernel test can
guard.

### Route every surface through the turn's destination

- [x] Move `Destination` out of `internal/kernel/approvals` into its own kernel
      package; approvals, events, and the turn orchestrator are all consumers,
      and none of them is approvals-specific.
- [x] Put a typed `Destination` on `events.Event` and let each surface
      construct its own, replacing `events.Message.ChatID`'s dual meaning.
      `destinationFromEvent`'s `event.Source == "web"` check and
      `decodeMessage`'s Telegram-owner default both disappear.
- [ ] Introduce a canonical `config.Owner.ID` instead of using
      `config.Telegram.OwnerID` (13 non-test call sites) as the system-wide
      identity; the Telegram adapter maps its numeric ID onto it. A web-only
      deployment must not need a Telegram owner ID configured.

### Split bootstrap into a core and its surfaces

- [ ] Move the turn orchestrator (`handleMessage`, `handleHeartbeat`,
      `handleApproval`, `messageHandlingPolicy`, and the read-only/heartbeat
      tool allowlists) out of `internal/bootstrap/app_events.go` into a kernel
      `TurnService` that accepts a neutral turn request (destination, text,
      policy). Telegram and web then become peers that each only build that
      request.
  - [ ] Land the narrowed unprompted-turn invariant as a kernel test, per
        "Let scheduled and heartbeat turns propose changes" above.
- [ ] Extract the command surface (`CommandService`, catalog, `CommandResult`)
      into its own package, and the HTTP surface (`web.go`, `chat.go`,
      `server.go`) into another. `internal/bootstrap` keeps wiring only.
- [ ] Add a `ports.ThreadStore` (create/list/get/set-title over a neutral
      `Thread` type). Today `web.go` and `chat.go` depend on the concrete
      `*memorysqlite.Store` and its adapter-owned `sqlite.Thread` type for a
      concept `ports.MemoryStore` doesn't model at all. The thread is also
      where an attached workspace belongs, so this blocks "Attach the
      workspace to the session, not the run."
- [ ] Give each surface a narrow interface onto the core rather than the whole
      36-field `App` struct.
- [ ] Make the proactive-output surface explicit configuration. `handleHeartbeat`
      delivers to the Telegram owner chat ID directly and
      `events.TypeScheduledMessage` inherits the Telegram default, so "which
      channel receives unprompted output" is currently a fallthrough rather
      than a decision.

## P1: Make context and capabilities inspectable

- [ ] Add a deterministic `/capabilities` view showing the selected reasoning
      model, registered assistant tools, configured repositories, enabled
      integrations, and implementation-loop readiness.
- [ ] Add a deterministic `/context` view showing injected bytes or estimated
      tokens per context file, recent-history and session-context sizes,
      tool-schema overhead, truncation markers, and the known context limit and
      remaining budget.
- [ ] Extend `/runs` detail with the model, base revision, phase, provider session
      ID, elapsed time, and validation status.
- [ ] Derive every diagnostic from bootstrap and persisted runtime state. Never
      expose credentials, raw environment contents, or credential paths.

## P1: Harden durable context and recall

- [ ] Give `USER.md` and `MEMORY.md` separate injected-size budgets instead of
      sharing the store-wide cap with `SOUL.md`.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.
- [ ] Keep recalled excerpts bounded, redacted, and explicitly marked as stale
      historical context rather than current authority. (Superseded by the
      SQLite-backed conversation memory work, now landed — durable, searchable
      recall is a database, not a file-backed design, at the owner's explicit
      direction; see
      `docs/superpowers/specs/2026-07-23-sqlite-memory-db-design.md` for why.)

Durable-context roles remain fixed: `SOUL.md` describes Eggy's identity and
tone, `USER.md` holds stable owner preferences, and `MEMORY.md` holds compact
facts, decisions, and reusable lessons. None may override runtime policy or
grant capabilities.

## P2: Build plug-and-play capabilities

See [`docs/adr/0005-procedural-skills.md`](docs/adr/0005-procedural-skills.md)
for the skill format and approval flow. New task workflows belong in isolated,
on-demand procedural skills; new providers and integrations belong in compiled
adapter packages wired only in `internal/bootstrap`. Neither needs a generic,
runtime-loaded plugin mechanism, and neither may redefine a kernel-owned
primitive tool.

- [ ] Store repeatable workflows and troubleshooting procedures in skills rather
      than expanding `MEMORY.md`. (Mechanism is in place; nothing yet steers
      the agent to prefer this over a `MEMORY.md` write in practice.)
- [ ] Add read-only `/skills browse <repo-url>` (lists `**/SKILL.md` paths,
      installs nothing) and `/skills clone <repo-url> <path>` (fetches one
      file, opens the normal approval request with the fetched body attached)
      instead of a bulk importer.

## P2: Improve run recovery and rollback

- [ ] When the owner continues an existing pull request, use its branch as the
      run base instead of branching from trunk and opening a duplicate pull
      request.
- [ ] Track the open pull request associated with each run so a later continuation
      can resolve it without relying only on repository and instruction text.
      Also blocks "Close the loop with pull-request checks" under P0.
- [ ] Save a bounded patch and validation artifact before workspace cleanup so
      rejected or interrupted work remains inspectable without retaining the
      full checkout.
- [ ] Add cleanup and retention diagnostics for abandoned workspaces and
      provider sessions.
- [ ] Add an explicit discard operation that cannot affect the owner's checkout.
- [ ] Evaluate a "retry from base" operation for contaminated or partially failed
      workspaces.

The immutable base commit is the pre-run checkpoint. Resumption always requires
a new owner instruction, and rollback stays inside the isolated run workspace;
it must never destructively modify the owner's checkout.

## P3: Evaluate stronger execution isolation

- [ ] Evaluate container-per-run isolation while keeping the current
      trusted-repository assumption explicit.
- [ ] If adopted, run as a non-root user with explicit mounts, dropped
      capabilities, bounded resources, and an explicit network policy.
- [ ] Keep credentials outside coding workspaces and forward only the minimum
      environment required by each subprocess.

## Standing constraints

Every roadmap item must preserve these properties:

- `internal/kernel` and `internal/ports` remain provider-neutral; adapters and
  tools are registered only in `internal/bootstrap`.
- The primitive tool surface (`read_file`, `terminal`, `patch`, `write_file`)
  is kernel-owned and defined exactly once. Adapters, MCP servers, and skills
  extend around it with namespaced tools and never shadow a primitive.
- Config, state, context, and session stores retain file locking and atomic
  writes. Existing `/data/state.json` files remain compatible or receive an
  explicit, tested schema migration.
- Telegram retains webhook authentication, owner allowlisting, and update
  deduplication.
- Repository execution retains root and path restrictions, environment
  allowlisting, timeouts, output limits, process-group cancellation, isolated
  workspaces, and cleanup.
- Ambiguous requests are clarified before a modifying workflow starts. Progress
  is streamed as normalized provider-neutral events, and Eggy independently
  captures the complete final diff and validation evidence before shipping.
- Durable context retains active-secret filtering and secret-like content
  rejection.
- Calendar mutations retain explicit owner approval, OAuth token encryption,
  idempotency, and ETag binding.
- Commit, push, and pull-request creation retain independent payload-bound
  authorization; protected branches remain unpushable even with approval. Eggy
  never merges a pull request.
- Unprompted turns (scheduled, heartbeat) may only *propose* repository
  changes: isolated branch, draft pull request, never a base branch. Work on an
  owner-facing branch remains explicitly owner-triggered. The invariant that
  holds unconditionally is that nothing lands without payload-bound
  authorization and a human-reviewed pull request.
- Operational state remains file-backed, so production runs exactly one
  `eggyd` replica.
- Every capability has a small, swappable boundary: task workflows are
  on-demand skills, while providers and integrations are compiled adapters with
  explicit bootstrap configuration. Provider payloads, credentials, channel
  formatting, and CLI protocols remain inside adapters; no capability may load
  arbitrary native code at runtime.
- The capability manifest stays small and reflects only the tools actually
  available to the current turn.
- Changes are developed test-first and verified with focused tests followed by
  `make fmt vet test race build`; run `make smoke` when Docker is available.
