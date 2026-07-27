# Eggy roadmap

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; completed implementation history remains in git.

Priorities are ordered by urgency, then by dependency. Remove an item when its
implementation and focused tests have landed — do not leave it here as a record
of finished work.

## P0: Finish the unified loop

There is one `agent.Loop` (`internal/kernel/agent/loop.go`), one kernel-owned
primitive tool set, and one termination condition: the model stops calling
tools. Shipping is an action (`propose_change`) rather than a run outcome, and
a workspace belongs to the thread rather than the run. Three gaps remain.

### Give every turn a durable transcript

- [ ] Move the checkpoint/compaction logic and the append-only event transcript
      out of `ImplementationSessions` (`/data/sessions/<id>/`) into
      `agent.Loop`, so every turn gets a durable transcript and a compaction
      checkpoint rather than only an editing session. Today `App.turnEvents`
      wires the loop's events into the session transcript only when the thread
      has an open editing session; a thread with no workspace still has no
      durable transcript.
- [ ] Replace the fixed `maxToolStepsPerTurn` guard (`internal/bootstrap/app.go:67`)
      with a context-budget checkpoint: the step limit stops meaning "how much
      work fits in a turn" and starts meaning "when to compact." It is already
      a single constant in one place.

### Close the loop with pull-request checks

- [ ] Add a checks-completed event that resumes the still-open thread workspace
      for the pull request whose checks failed. Without this, self-improvement
      is one-shot; with it, it is a loop.
- [ ] Reuse `repository_github`'s `checks` read path for the evidence rather
      than adding a second GitHub surface.
- [ ] Depends on "Track the open pull request associated with each run" under
      P2: Improve run recovery and rollback.

### Let scheduled and heartbeat turns propose changes

The invariant "scheduled and heartbeat turns cannot reach repository write
tools" was always a proxy for the one that matters: *nothing lands without a
payload-bound authorization and a human-reviewed pull request*. Narrow it
deliberately rather than keeping the proxy.

- [ ] Add `self_repository` to `agent.CapabilityManifest`
      (`internal/kernel/agent/prompt.go:12`) so the agent knows which registered
      repository is its own body, and that `AGENTS.md` and
      `docs/ARCHITECTURE.md` describe it.
- [ ] Give heartbeat and scheduled turns a `propose_improvement` path: isolated
      branch, draft pull request, never a base branch. Arbitrary repository
      writes from an unprompted turn stay barred.
- [ ] Rework the `readOnlyRunOptions`/`heartbeatRunOptions` allowlists
      (`internal/bootstrap/app_events.go:117-145`) around the narrowed
      invariant, and land it as a kernel test once the turn orchestrator moves
      (see "Split bootstrap into a core and its surfaces"): an unprompted turn
      cannot target a base branch, cannot open a non-draft pull request, and
      still cannot reach MCP tools.
- [ ] Update `docs/ARCHITECTURE.md`'s safety-invariant list and the standing
      constraints at the bottom of this file together with the code, not after.

## P0: Separate the agent core from its control surfaces

The delivery seam is clean: `destination` is its own kernel package,
`events.Event` carries a typed `Destination` each surface builds for itself,
`config.Owner.ID` is the system-wide identity, and the Telegram block is
optional so a web-only deployment boots without it.

What remains is above it. `internal/bootstrap` is ~11.4k lines holding four
layers at once: the composition root (`app.go`), the turn orchestrator
(`app_events.go`), the command surface (`commands*.go`, `command_catalog.go`,
`command_result.go`), and the HTTP surface (`web.go`, `chat.go`, `server.go`).
The turn orchestrator is core agentic behavior, not wiring — including the
read-only/heartbeat allowlists that encode a documented safety invariant in the
one package no kernel test can guard.

### Split bootstrap into a core and its surfaces

- [ ] Move the turn orchestrator (`handleMessage`, `handleHeartbeat`,
      `handleApproval`, `messageHandlingPolicy`, and the read-only/heartbeat
      tool allowlists) out of `internal/bootstrap/app_events.go` into a kernel
      `TurnService` that accepts a neutral turn request (destination, text,
      policy). Telegram and web then become peers that each only build that
      request.
  - [ ] Land the narrowed unprompted-turn invariant as a kernel test, per "Let
        scheduled and heartbeat turns propose changes" above.
- [ ] Extract the command surface (`CommandService`, catalog, `CommandResult`)
      into its own package, and the HTTP surface (`web.go`, `chat.go`,
      `server.go`) into another. `internal/bootstrap` keeps wiring only.
- [ ] Give each surface a narrow interface onto the core rather than the whole
      36-field `App` struct.

Unprompted output stays Telegram-only, deliberately. Heartbeat, scheduled agent
turns, and scheduled messages all stamp `proactiveDestination()`
(`app_events.go`) on ctx explicitly rather than relying on a channel fallback.
The web UI is a pull surface the owner opens, not one Eggy pushes to, and a
single proactive channel keeps `HeartbeatPolicy`'s quiet-hours and weekly-limit
accounting meaningful rather than per-channel. A web-only deployment therefore
produces no unprompted output at all. Revisit only if the web UI gains real
push delivery; turning this into *configuration* would change that one
function.

## P1: Make context and capabilities inspectable

- [ ] Add a deterministic `/capabilities` view showing the selected reasoning
      model, registered assistant tools, configured repositories, enabled
      integrations, and implementation-loop readiness.
- [ ] Add a deterministic `/context` view showing injected bytes or estimated
      tokens per context file, recent-history and session-context sizes,
      tool-schema overhead, truncation markers, and the known context limit and
      remaining budget.
- [ ] Extend `/runs` detail with the model, base revision, phase, provider
      session ID, elapsed time, and validation status.
- [ ] Derive every diagnostic from bootstrap and persisted runtime state. Never
      expose credentials, raw environment contents, or credential paths.

## P1: Harden durable context and recall

- [ ] Give `USER.md` and `MEMORY.md` separate injected-size budgets instead of
      sharing the store-wide cap with `SOUL.md`.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.

Durable-context roles remain fixed: `SOUL.md` describes Eggy's identity and
tone, `USER.md` holds stable owner preferences, and `MEMORY.md` holds compact
facts, decisions, and reusable lessons. None may override runtime policy or
grant capabilities. Conversation recall is served by the SQLite-backed memory
store (`plugins/memory/sqlite`), not by file-backed excerpts.

## P2: Build plug-and-play capabilities

New task workflows belong in isolated, on-demand procedural skills; new
providers and integrations belong in compiled packages under `plugins/`, wired
only in `internal/bootstrap`. Neither needs a generic, runtime-loaded plugin
mechanism, and neither may redefine a kernel-owned primitive tool.

- [ ] Store repeatable workflows and troubleshooting procedures in skills rather
      than expanding `MEMORY.md`. (Mechanism is in place; nothing yet steers the
      agent to prefer this over a `MEMORY.md` write in practice.)
- [ ] Add read-only `/skills browse <repo-url>` (lists `**/SKILL.md` paths,
      installs nothing) and `/skills clone <repo-url> <path>` (fetches one file,
      opens the normal approval request with the fetched body attached) instead
      of a bulk importer.

## P2: Improve run recovery and rollback

- [ ] When the owner continues an existing pull request, use its branch as the
      run base instead of branching from trunk and opening a duplicate pull
      request.
- [ ] Track the open pull request associated with each run so a later
      continuation can resolve it without relying only on repository and
      instruction text. Also blocks "Close the loop with pull-request checks"
      under P0.
- [ ] Save a bounded patch and validation artifact before workspace cleanup so
      rejected or interrupted work remains inspectable without retaining the
      full checkout.
- [ ] Add cleanup and retention diagnostics for abandoned workspaces and
      provider sessions.
- [ ] Add an explicit discard operation that cannot affect the owner's checkout.
- [ ] Evaluate a "retry from base" operation for contaminated or partially
      failed workspaces.

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

## Operational follow-ups

- [ ] Reset Railway's deployed `/data/config.yaml` so the next boot generates the
      current unversioned config shape. Manual deployment step, not a code
      change.

## Standing constraints

Every roadmap item must preserve these properties:

- `internal/kernel` and `internal/ports` remain provider-neutral; adapters live
  under `plugins/` and are registered only in `internal/bootstrap`.
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
- Operational state remains file-backed, so production runs exactly one `eggyd`
  replica.
- Every capability has a small, swappable boundary: task workflows are
  on-demand skills, while providers and integrations are compiled plugins with
  explicit bootstrap configuration. Provider payloads, credentials, channel
  formatting, and CLI protocols remain inside plugins; no capability may load
  arbitrary native code at runtime.
- The capability manifest stays small and reflects only the tools actually
  available to the current turn.
- Changes are developed test-first and verified with focused tests followed by
  `make fmt vet test race build`; run `make smoke` when Docker is available.
