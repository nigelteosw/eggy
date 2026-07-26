# Eggy roadmap

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; durable design rationale belongs in `docs/adr/`;
completed implementation history remains in git.

Priorities are ordered by urgency, then by dependency. Check an item only when
its implementation and focused tests have landed.

## P0: Finish the architecture simplification

### Simplify repository shipping

`ShippingService` (`internal/kernel/services/shipping.go:12-23`) is the only
kernel service still wired with setters instead of constructor injection:
`NewShippingService` takes `policy ports.ApprovalPolicy`, then
`internal/bootstrap/app.go:178-179` calls `SetApprovalRequester` and
`SetApprovalDecider` right after construction to fill in the other two roles —
always with the same `app.approvals` instance. `RepositoriesService`,
`CalendarService`, and `SkillsService` all take their `ApprovalRequester`
through the constructor; `ShippingService` is the sole outlier. `Ship()`
(shipping.go:48-99) then does Request → `decider.Decide(id, true)` → Execute
three times in a row for commit/push/PR, ceremony left over from when a human
tapped Approve/Reject in Telegram. That old path also left dead code in
`App.handleApproval` (`internal/bootstrap/app.go:520-566`): a rejection-cleanup
branch and a post-PR-creation cleanup branch guarded by a comment admitting
"these branches only remain reachable for a pending approval left over from
before that change."

- [x] Keep `ShippingService` as Eggy's single repository-write boundary. Only
      the implementation loop may call it; coding agents cannot commit, push,
      or create pull requests directly.
- [x] Replace its three approval roles and setter-based setup with one narrow
      constructor-injected authorization dependency that issues, automatically
      decides, and authorizes an exact action and payload.
  - [x] Add `ApprovalService.RequestAndApprove(ctx, action, payload, summary)
        (approvals.Approval, error)`, doing the create-then-immediately-approve
        sequence as one `store.Update`, replacing today's separate `Request`
        call plus `decider.Decide(id, true)` call.
  - [x] Define one narrow interface (e.g. `ShippingAuthorizer`) with
        `RequestAndApprove` and `Authorize` methods; replace
        `ShippingService`'s `policy`, `requester`, and `decider` fields with a
        single field of this type.
  - [x] Take it as a parameter of `NewShippingService`; delete
        `SetApprovalRequester`, `SetApprovalDecider`, the `ApprovalDecider`
        interface, and both setter call sites in `internal/bootstrap/app.go`.
  - [x] Update `Ship()` to call `RequestAndApprove` once per action (commit,
        push, create-pull-request) instead of Request-then-Decide, and update
        `RequestCommit`/`RequestPush`/`RequestPullRequest` and their callers
        accordingly.
- [x] Keep commit, push, and pull-request creation as separate authorized
      operations. Preserve expiry, action and payload matching, full-diff
      binding, branch/head and remote-head checks, and protected-branch denial.
  - [x] Keep `Commit`/`Push`/`CreatePullRequest` calling `Authorize`
        independently per action with the workspace/diff/head re-checks that
        already precede each one; the merged `ShippingAuthorizer` interface
        does not collapse these into one call.
- [x] Remove obsolete pending shipping approvals at startup or migrate them;
      remove shipping from Telegram's callback executor map and delete its
      compatibility-only cleanup paths.
  - [x] Add a startup step (`App.Run`) that loads state and calls
        `ApprovalService.Invalidate` on any `Pending` approval whose `Action`
        is `Commit`, `Push`, or `CreatePR` — these can only be leftovers from
        the retired manual-tap flow, since no code path still creates a
        pending shipping approval that waits for a human `Decide` call.
  - [x] Remove the `approvals.Commit`, `approvals.Push`, and `approvals.CreatePR`
        entries from `app.approvalExecutors`.
  - [x] Delete the rejection-cleanup branch and the post-`CreatePR`-cleanup
        branch in `App.handleApproval`, along with the comment explaining
        they're compatibility-only.
- [x] Keep Telegram approval callbacks for repository registration and Calendar
      mutations. Pull-request merging remains unsupported.
  - [x] Leave the `AddRepository`, `CalendarCreate`, `CalendarUpdate`,
        `CalendarDelete`, `SkillWrite`, and `SkillDelete` entries in
        `app.approvalExecutors` untouched; only the three shipping actions move
        off the human-tap callback path.
- [x] Add an end-to-end test proving the implementation loop cannot bypass any
      repository-write authorization check.
  - [x] Add a test in `shipping_test.go` (`TestShippingBlocksTamperedOrProtectedActionsEndToEnd`)
        that drives a full run through `Ship()` against a real
        `ApprovalService` and asserts that a tampered diff, a moved
        branch/head, a moved remote head, and a protected-branch push are
        each blocked — exercising `RequestAndApprove` and `Authorize`
        together end to end, not just the already-covered individual
        `Commit`/`Push`/`CreatePullRequest` checks.

### Deployment follow-up

- [ ] Reset Railway's deployed `/data/config.yaml` so the next boot generates
      the current unversioned config shape. This is a manual deployment step.

## P0: Separate the agent core from its control surfaces

Telegram and the web UI are meant to be independent, equal channels into one
agent core, but the seam between them is still Telegram-shaped. `ports.Channel`
carries a `chatID` parameter that `routedChannel` documents as ignored
(`internal/bootstrap/routed_channel.go:14-19`), so every caller passes a value
with no effect; `AnswerCallback` is a Telegram button-tap concept webchat
no-ops and `routedChannel` hardcodes to Telegram; `config.Telegram.OwnerID` is
the universal owner identity even for web-only events; and `events.Message`'s
`ChatID` means "Telegram chat" or "web thread" depending on a string comparison
against `event.Source` (`app_events.go:605-610`). The live consequence is that
`telegram.ProgressTracker` must invent `context.Background()`
(`progress_tracker.go:36`) because the progress callback type
`func(ports.CodingProgress)` carries no context, so `DestinationFromContext`
finds nothing and a web-initiated implementation run streams its whole progress
timeline to Telegram instead of the web thread.

Above that, `internal/bootstrap` is 10.5k lines holding four layers at once:
the composition root (`app.go`), the turn orchestrator (`app_events.go`), the
command surface (`commands*.go`, `command_catalog.go`, `command_result.go`),
and the HTTP surface (`web.go`, `chat.go`, `server.go`). The turn orchestrator
is core agentic behavior, not wiring — including the
`readOnlyRunOptions`/`heartbeatRunOptions` allowlists (`app_events.go:623-646`)
that encode a documented safety invariant in the one package no kernel test can
guard.

### Route every surface through the turn's destination

- [x] Thread a context through coding progress so a run reports to the surface
      that started it.
  - [x] Add `ports.ProgressReporter` (`func(context.Context, CodingProgress)`)
        and adopt it across `CodingService.Start`/`Resume`/`ResumeLatest`/
        `implement`/`reporter`, `Implementer.Implement`, and the
        `RepositoryModifier`/`RepositoryResumer` interfaces.
  - [x] Pass the per-turn `ctx` already available in the tool's `Execute`, and
        delete `ProgressTracker.Deliver`'s `context.Background()`.
  - [x] Cover it: a web-destination turn's progress reaches the web thread and
        never Telegram.
- [x] Move `ProgressTracker` out of the Telegram adapter into
      `internal/adapters/channels/channelutil`; it depends only on
      `ports.Channel`, exactly like the `DeliverOutcome` and `StartTyping`
      helpers already there.
  - [ ] Its `fallbackChatID` is still the Telegram owner ID, which a
        web-only deployment would use as a thread ID. Removing `chatID` from
        `ports.Channel` (below) retires it.
- [ ] Remove the `chatID` parameter from `ports.Channel`. The Telegram adapter
      binds its owner chat ID at construction; webchat resolves
      `Destination.ThreadID` from the context. Drop the now-unused `owner`
      argument from `skillProposeTool`, `calendarTools`, `NewProgressTracker`,
      and the `CommandService.owner` field.
- [ ] Take `AnswerCallback` off `ports.Channel` entirely — acking a callback
      query is a detail of *receiving* a Telegram update, so it belongs in
      `telegram`'s webhook handler, not in a delivery port every other surface
      must no-op. Consider splitting what remains into `Channel`
      (`Deliver`/`DeliverApproval`) plus optional `TrackableChannel`
      (`DeliverTrackable`/`EditText`) and `TypingChannel`.
- [ ] Move `Destination` out of `internal/kernel/approvals` into its own kernel
      package; approvals, events, and the turn orchestrator are all consumers,
      and none of them is approvals-specific.
- [ ] Put a typed `Destination` on `events.Event` and let each surface
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
  - [ ] Land the allowlist invariant as a kernel test: a scheduled or
        heartbeat turn cannot reach `repository_modify`, `repository_continue`,
        or any MCP tool.
- [ ] Extract the command surface (`CommandService`, catalog, `CommandResult`)
      into its own package, and the HTTP surface (`web.go`, `chat.go`,
      `server.go`) into another. `internal/bootstrap` keeps wiring only.
- [ ] Add a `ports.ThreadStore` (create/list/get/set-title over a neutral
      `Thread` type). Today `web.go` and `chat.go` depend on the concrete
      `*memorysqlite.Store` and its adapter-owned `sqlite.Thread` type for a
      concept `ports.MemoryStore` doesn't model at all.
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
      historical context rather than current authority. (Superseded by
      "P1: Add SQLite-backed conversation memory with vector search" below —
      durable, searchable recall is now a database, not a file-backed design,
      at the owner's explicit direction; see
      `docs/superpowers/specs/2026-07-23-sqlite-memory-db-design.md` for why.)

Durable-context roles remain fixed: `SOUL.md` describes Eggy's identity and
tone, `USER.md` holds stable owner preferences, and `MEMORY.md` holds compact
facts, decisions, and reusable lessons. None may override runtime policy or
grant capabilities.

## P1: Isolate heartbeat and scheduled work

- [x] Give heartbeat turns a small, heartbeat-specific context rather than the
      full conversational history.
- [x] Run heartbeat evaluation in an isolated conversation so old chat
      instructions cannot be revived accidentally.
- [x] Separate silent context curation from the quiet-hours and weekly-message
      gate used for proactive Telegram check-ins.
- [x] Skip or defer heartbeats while an implementation run or another protected
      workflow is active.
- [x] Ensure that "nothing useful to report" sends no Telegram message.
- [x] Distinguish deterministic scheduled commands from scheduled agent turns;
      run reminders, watchdogs, and pre-rendered notifications without a model
      call.
- [x] Require scheduled agent prompts to be self-contained and start them
      without ambient chat history.
- [x] Evaluate a human-editable `HEARTBEAT.md` checklist while keeping timing,
      timezone, quiet hours, limits, and prohibited actions in deterministic
      policy.

## P1: Add SQLite-backed conversation memory with vector search

See [`docs/superpowers/specs/2026-07-23-sqlite-memory-db-design.md`](docs/superpowers/specs/2026-07-23-sqlite-memory-db-design.md).
Reopens the "no database for transcript recall" decision at the owner's
explicit direction; additive only — `config.yaml`, `state.json`, and the
curated `SOUL.md`/`USER.md`/`MEMORY.md` documents are unchanged. Durable,
searchable conversation history is now an explicit web-chat building block
needed to make chats useful across sessions, not a database added merely to
mirror another harness. The approved data policy is raw write/read-time
redaction, with no pruning yet.

- [x] Complete the approved reversal: durable, searchable transcript memory
      now supports useful web chat across sessions rather than mirroring
      another harness. It stores raw conversation content and redacts only at
      recall time; no retention/pruning policy is implemented yet.
- [x] Confirm FTS5 works against the pinned `modernc.org/sqlite` version
      before anything else is built on it; fall back to a plain `LIKE` query
      if not.
- [x] Add `ports.MemoryStore` (`WriteMessage`, `SearchText`, `SearchSimilar`,
      `PendingEmbeddings`, `SetEmbedding`) and
      `internal/adapters/memory/sqlite`, storage-only, no CGO, no reference to
      `Embedder`. `SearchSimilar` scores only a bounded recency window, never
      an unbounded full-table scan.
- [x] Add `ports.Embedder` and an `Embed` method on the existing
      `internal/adapters/models/openaicompat` `Model` type (reuse its
      HTTP-client/credential plumbing rather than a new sibling package),
      configured via a new `embeddings:` config section.
- [x] Add `services.MemoryEmbeddingWorker`: orchestrates `MemoryStore` +
      `Embedder` (polls `PendingEmbeddings`, calls `Embed`, writes back via
      `SetEmbedding`) on the same periodic-loop machinery the
      scheduler/heartbeat already use. Only constructed when `embeddings` is
      configured; conversation storage and full-text search work with zero
      configuration either way.
- [x] Add a `recall_conversation` agent tool: bounded, redacted (reuse
      `SecretGuard`-style scrubbing) results, explicitly framed as historical
      context, never auto-injected into ordinary turn context.
- [x] Decide and implement the two open calls the spec deliberately left to
      the owner rather than defaulting silently: write-time secret handling
      (redact/reject at write time, or accept today's read-time-only
      redaction as sufficient) and a retention/pruning story (bounded
      row-count or age-based eviction, or explicitly "not needed yet"). The
      approved choices are raw write/read-time redaction and no pruning yet.

## P2: Build plug-and-play capabilities

See [`docs/adr/0005-procedural-skills.md`](docs/adr/0005-procedural-skills.md)
for the skill format and approval flow. New task workflows belong in isolated,
on-demand procedural skills; new providers and integrations belong in compiled
adapter packages wired only in `internal/bootstrap`. Neither needs a generic,
runtime-loaded plugin mechanism.

- [x] Add a `skills.Store` adapter over flat `data_dir/skills/<name>.md` files
      (`name` + `description` frontmatter only; no bundled scripts/assets),
      with atomic+locked writes, a size cap, and name-pattern validation.
- [x] Inject only the compact `name: description` index into ordinary context;
      add a `skill_read` agent tool that loads one skill's full body on demand
      when its description matches the current task.
- [ ] Store repeatable workflows and troubleshooting procedures in skills rather
      than expanding `MEMORY.md`. (Mechanism is in place; nothing yet steers
      the agent to prefer this over a `MEMORY.md` write in practice.)
- [x] Let Eggy propose a skill (name, description, body) after a successful
      complex workflow, recovered failure, or owner correction, via
      `ApprovalService.Request` — the same digest-bound approval flow as
      Calendar mutations and `add_repository`, not the freely agent-writable
      memory path. (`skill_propose` tool.)
- [x] Do not expose `skill_write`/`skill_delete` as directly callable agent
      tools; both go through approval `Decide` before any write happens.
- [x] Validate skill names, sizes, and forbidden secret content with the
      existing `SecretGuard`.
- [x] Add a `/skills` command family mirroring `/memory`: list, `show <name>`,
      `add <name> <description> <content>`, `edit <name> <content>`, `remove
      <name>` — owner-initiated adds/edits/removals still open an approval
      request rather than writing immediately.
- [x] Track a `DisabledSkills` set in `state.json`; add freely agent-callable
      `skill_disable`/`skill_enable` tools (no approval — reversible, content
      untouched) and drop disabled skills from the compact index and the
      steering list.
- [ ] Add read-only `/skills browse <repo-url>` (lists `**/SKILL.md` paths,
      installs nothing) and `/skills clone <repo-url> <path>` (fetches one
      file, opens the normal approval request with the fetched body attached)
      instead of a bulk importer.
- [x] Extend `CapabilityManifest` with enabled skills' `name: description`
      pairs and render them as their own system message in
      `agent.BuildInstructions`; add one `hardRuntimePolicy` line telling the
      agent to check that list before non-trivial work and call `skill_read`
      on a match before following it.

## P2: Improve run recovery and rollback

- [ ] When the owner continues an existing pull request, use its branch as the
      run base instead of branching from trunk and opening a duplicate pull
      request.
- [ ] Track the open pull request associated with each run so a later continuation
      can resolve it without relying only on repository and instruction text.
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
  authorization; protected branches remain unpushable even with approval.
- Scheduled and heartbeat turns cannot modify repositories. New and resumed
  implementation work remains explicitly owner-triggered.
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
