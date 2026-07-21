# Eggy roadmap

This checklist captures the practical lessons Eggy can adopt from OpenClaw,
Hermes Agent, and NanoClaw. It preserves Eggy's Go ports-and-adapters
architecture, file-backed state, trusted-repository model, and independent
approval gates.

## P0: Simplify the current architecture

Reduce accidental complexity before adding more capabilities. Keep the
ports-and-adapters modular monolith, but remove duplicated sources of truth,
unused extension points, compatibility paths that no longer serve a deployed
format, and documentation that describes superseded implementations.

Complete these changes in the order listed below. The first four are low-risk
cleanup and should land before the run/session persistence refactor. Every
state-shape change must preserve existing `/data/state.json` files through an
explicit, tested schema migration.

### 1. Remove dead task, state, and port types

The generic task subsystem has no production producer. Nothing creates a
`tasks.Task`; startup only scans an empty `State.Tasks` map and marks hypothetical
running tasks as interrupted. Coding runs and implementation sessions already
have their own lifecycle and recovery behavior.

- [x] Remove `internal/kernel/tasks/tasks.go`,
      `internal/kernel/services/task_service.go`, and their tests.
- [x] Remove `TaskService.RecoverInterrupted` from `App.Run`.
- [x] Remove `State.Tasks` through an explicit state-schema migration. Loading
      an existing state file containing `tasks` must remain safe and must not
      corrupt any unrelated field.
- [x] Remove `State.SelectedRepository`; production code never sets it. Update
      deterministic status output to report configured repositories or another
      value backed by live state instead of an always-empty selected value.
- [x] Remove `CodingRuntimeState` and `State.Coding`; `SelectedAgent` belongs to
      the retired configurable coding-agent path and has no production writer.
- [x] Remove `State.ConversationSummary` unless a real summary producer is
      introduced in the same change. The current runtime reads and clears the
      field but never creates a summary.
- [x] Remove the unused `TriggerSource` and `StreamingRunner` ports.
- [x] Remove the unused `ScheduleHeartbeat` enum value if no persisted schedule
      uses it; otherwise migrate it explicitly before deletion.
- [x] Review fields on `ImplementationSession` such as `Title`, `Model`, and
      `PromptVersion`, and delete fields that have no writer or planned reader.
- [x] Add a focused migration test using a representative current production
      state file containing repositories, approvals, schedules, Calendar auth,
      model selection, usage, and coding history.
- [x] Verify that startup recovery still covers active implementation sessions
      and pending schedules after the generic task recovery is removed.

### 2. Create one command contract for Telegram and the CLI

Command names, parsing, validation, help, and output are currently duplicated
across `internal/bootstrap/commands.go`, `cmd/eggy/config.go`, Telegram's bot
command list, and hand-maintained help text. This duplication is already causing
Telegram and CLI grammar and output to drift.

- [x] Define one command catalog containing each command's name, description,
      subcommands, arguments, examples, handler, and response intent.
- [x] Keep Telegram's natural `key=value` syntax for named values, for example
      `/config set model alias=deepseek-pro provider=deepseek
      model=deepseek-v4-pro reasoning_efforts=low,medium,high,max`.
- [x] Keep conventional CLI flags, for example `eggy config set model
      --alias=deepseek-pro --provider=deepseek --model=deepseek-v4-pro
      --reasoning-efforts=low,medium,high,max`.
- [x] Parse both surfaces into the same validated command request. Telegram must
      not spawn the `eggy` executable as a subprocess.
- [x] Route `eggy config` through the shared config command handler while
      preserving its ability to work without constructing the full runtime or
      requiring locally available provider credentials.
- [x] Replace ad-hoc response strings with a structured command result containing
      a state (`success`, `info`, `warning`, `error`, or `help`), title, fields or
      rows, explanatory detail, and relevant next commands.
- [x] Render the same result as clean plain text for the CLI and safe HTML for
      Telegram. CLI output must remain readable when redirected to a file or
      pipe and must not require colour support.
- [x] Generate `/help`, `eggy help`, and Telegram autocomplete metadata from the
      shared command catalog so the lists cannot drift.
- [x] Keep legacy positional Telegram forms temporarily where removing them
      would break saved commands, but show only the canonical syntax in new help
      and confirmation output.
- [x] Give every direct command enough context to answer: what is the current
      state, what did Eggy just do, and what can the owner do next?
- [x] Improve empty states: no repositories should show the exact add command;
      no runs should explain how an implementation run starts; no prompts should
      explain what custom prompts do; no schedules should explain how scheduling
      is requested.
- [x] Improve command-family output:
  - [x] `status`: show configured repositories, active runs, pending approvals,
        schedules, active model, and relevant next actions from real state;
  - [x] `repositories`: show repository name, base branch, protected branches,
        and whether an add request is awaiting owner approval;
  - [x] `runs`: show run ID, repository, phase/status, validation state, and the
        correct continue or stop command when applicable;
  - [x] `continue` and `stop`: explain whether work remains resumable and show
        the pull-request URL when shipping completes;
  - [x] `schedules`: show instruction/purpose, next run, enabled state, and the
        owner timezone;
  - [x] `memory` and `clear`: distinguish durable `USER.md`/`MEMORY.md` content
        from disposable recent conversation context;
  - [x] `prompts`: explain how named prompts affect future turns and show exact
        show, set, and remove examples;
  - [x] `model`: show the active alias, reasoning effort, allowed effort levels,
        configured alternatives, and exact change commands;
  - [x] `config`: render providers, models, and Calendar settings as labelled
        fields; state that environment-variable names are references rather than
        secret values; clearly mark changes that require restart;
  - [x] `usage`: render per-model token categories clearly and retain the warning
        that local provider-reported totals are not billing records;
  - [x] `calendar_auth`: explain the authorization purpose and the ten-minute,
        single-use enrollment-link expiry;
  - [x] `restart`: explain that active implementation sessions are interrupted
        safely and can be resumed explicitly after Eggy returns.
- [x] Format malformed commands as actionable help with the missing or invalid
      field, a Telegram example, and a CLI example instead of a bare usage line.
- [x] Add parity tests proving equivalent Telegram and CLI input produces the
      same command request and semantic result.
- [x] Add renderer tests for escaping, long output, lists, links, errors, and
      plain-text fallback.
- [x] Add a catalog coverage test proving every registered Telegram command has
      CLI help and a handler.

### 3. Reduce documentation to current sources of truth

Historical Superpowers plans and superseded specs contain nearly as many lines
as the production Go code. Several describe removed architecture, including
Flash/Pro routing, a Codex CLI coding adapter, and manual Telegram approvals for
each shipping step. They now make the current system harder to understand.

- [ ] Keep `README.md` as the current operator guide and update it whenever a
      command, runtime path, deployment requirement, or supported capability
      changes.
- [ ] Replace the stale MVP spec with one concise current architecture document
      describing the native implementation loop, configurable model aliases,
      automatic shipping flow with independent authorization checks, durable
      sessions, and the actual Telegram/CLI surfaces.
- [ ] Preserve durable architectural decisions as small ADRs when their rationale
      still matters; do not retain full step-by-step implementation plans as
      active documentation.
- [ ] Archive outside the active documentation tree or remove completed files in
      `docs/superpowers/plans/` and superseded files in
      `docs/superpowers/specs/`.
- [ ] Reconcile this roadmap with the implementation. Mark implemented safety
      work complete, delete obsolete aspirations, and avoid keeping unchecked
      items that merely restate existing behavior.
- [ ] Remove stale references to `/new`, a future TUI, Codex device authentication,
      Flash/Pro automatic escalation, and manual shipping taps unless those
      features are deliberately restored.
- [ ] Add a lightweight documentation check that validates command names and
      important file paths against the shared command catalog or current source.

### 4. Remove version-1 config compatibility after deployment verification

First boot now creates config version 2, and config mutation already refuses
version 1. The loader, normalizer, defaults, validation, and YAML marshaler still
carry a parallel version-1 model.

- [ ] Inspect the deployed `/data/config.yaml` and confirm it is version 2 before
      removing any compatibility path.
- [ ] If production still uses version 1, provide and run a one-time, atomic,
      backed-up migration to version 2 before deleting support.
- [ ] Remove `legacyConfigDocument`, `ModelsConfig`, `ModelConfig`,
      `EscalationConfig`, `legacyModels`, `normalizeLegacyConfig`, version-1
      validation/default branches, and version-1 YAML marshaling.
- [ ] Make version 2 the only accepted config format and return a concise
      migration error for an old file rather than silently guessing.
- [ ] Retain strict known-field validation, atomic writes, file locking, secret
      indirection through environment-variable names, and Railway `PORT`
      override behavior.
- [ ] Verify first boot, config get/set/show, local startup, and Railway startup
      against the single config representation.

### 5. Make implementation sessions the single source of run truth

`CodingRun` in `state.json` and `ImplementationSession` under `/data/sessions`
duplicate run ID, repository, workspace, branch, base revision, status, and
timestamps. `CodingService` updates both and contains mismatch handling for when
the two copies diverge. This is the largest avoidable runtime complexity.

- [ ] Define one typed run/session aggregate containing repository, workspace,
      branch, base revision, current phase, validation, commit, pull request,
      timestamps, resumable context, and bounded event history.
- [ ] Use the implementation-session store as the canonical source for run
      metadata and lifecycle. Keep the append-only event log separate from the
      small metadata document so transcripts do not inflate `state.json`.
- [ ] Replace the stringly typed `CodingRun.Status` and the overlapping
      `ImplementationSessionStatus` values with one typed phase model.
- [ ] Rename or remove `awaiting_*_approval` phases that are only instantaneous
      internal milestones in the automatic shipping flow. Preserve useful
      `running`, `interrupted`, `blocked`, `committed`, `pushed`, `completed`,
      and `cancelled` states.
- [ ] Move validation evidence, commit hash, pull-request number/URL, and cleanup
      state into the canonical session record.
- [ ] Update `CodingService`, `ShippingService`, `/runs`, `/continue`, `/stop`,
      recovery, retention cleanup, progress delivery, and status reporting to
      read and write the canonical store only.
- [ ] Remove direct access such as `s.sessions.store.Update`; expose the minimal
      lifecycle operations required by the service boundary.
- [ ] Remove `State.CodingRuns` through an explicit state-schema migration after
      existing runs have been imported into the session store.
- [ ] Make the migration idempotent and crash-safe: rerunning it after an
      interrupted startup must not duplicate events, discard a diff, or make a
      resumable workspace unreachable.
- [ ] Block a migrated session clearly when its workspace, branch, or base
      revision no longer exists; never auto-replay implementation work.
- [ ] Keep session transcripts and uncommitted workspaces on the Railway volume
      and preserve explicit owner-triggered continuation.
- [ ] Add migration, restart, resume, shipping, cleanup, and corrupted-session
      tests before removing the old representation.

### 6. Simplify shipping authorization without weakening it

`ShippingService` currently receives the approval subsystem as three separate
roles (`policy`, `requester`, and `decider`), creates a pending approval,
immediately decides it, then authorizes the same action. `App` also keeps
shipping callback executors for pending approvals left by an older manual-tap
flow.

- [ ] Define one narrow shipping-authorization dependency that owns issuance,
      automatic decision, and later authorization of the exact payload without
      exposing three setter-injected roles.
- [ ] Inject all required shipping dependencies through the constructor; remove
      `SetApprovalRequester`, `SetApprovalDecider`, and other partial-construction
      states.
- [ ] Preserve a distinct authorization record and check for commit, push, and
      pull-request creation. Automatic progression must not collapse the three
      actions into one broad permission.
- [ ] Preserve expiry, action matching, payload-digest binding, complete and
      untruncated diff binding, local branch/head checks, remote-head checks,
      and protected-branch denial.
- [ ] Add a state migration or startup invalidation for obsolete pending
      shipping approvals, then remove shipping from Telegram's callback executor
      map and delete compatibility-only cleanup branches.
- [ ] Keep explicit Telegram approval callbacks for repository registration and
      Calendar create/update/delete operations.
- [ ] Keep pull-request merging unsupported.
- [ ] Add tests proving a coding or model tool cannot bypass any protected
      shipping step after the plumbing is simplified.

### 7. Decide whether custom prompts earn their complexity

The fixed prompt sources are currently wrapped in a global priority registry,
although no production package extends that registry. Named custom prompts also
introduce another persistent instruction layer alongside `SOUL.md`, `USER.md`,
and `MEMORY.md`.

- [ ] Measure whether `/prompts` is used in the deployed workflow before making
      this a permanent product feature.
- [ ] If custom prompts are not used, remove `/prompts`, `NamedPrompt`, prompt
      CRUD methods from `ContextStore`, prompt-directory persistence, custom
      prompt injection, help entries, and their tests.
- [ ] If custom prompts are used, keep the feature but document its authority
      below hard runtime policy and current owner instructions.
- [ ] In either case, replace the mutable global `PromptSection` registry and
      `init` registration with a direct, explicit `BuildInstructions` sequence
      unless a real compiled extension consumes the registry.
- [ ] Keep the trust order obvious in code: hard runtime policy, capability
      manifest, `SOUL.md`, `USER.md`, `MEMORY.md`, optional custom prompts,
      trusted temporal context, then recent conversation.
- [ ] Preserve memory size indicators, secret filtering, and the rule that
      durable context cannot override the current owner instruction.

### Simplification invariants

Do not use cleanup as a reason to weaken the controls that make Eggy's trusted
single-owner model safe and recoverable.

- [ ] Keep `internal/kernel` and `internal/ports` provider-neutral and register
      adapters only in `internal/bootstrap`.
- [ ] Keep file locking and atomic writes for config, state, context, and session
      persistence.
- [ ] Keep Telegram webhook authentication, owner allowlisting, and update
      deduplication.
- [ ] Keep runner root restrictions, path validation, environment allowlisting,
      timeout and output bounds, process-group cancellation, and workspace
      cleanup.
- [ ] Keep active-secret filtering and secret-like content rejection for context
      writes.
- [ ] Keep Calendar mutation approvals, OAuth token encryption, idempotency, and
      ETag binding.
- [ ] Keep independent commit, push, and pull-request authorization checks and
      protected-branch denial.
- [ ] Keep scheduled and heartbeat turns unable to access repository-modifying
      tools; retain the explicit owner-trigger for new and resumed coding work.
- [ ] Keep exactly one `eggyd` replica while operational state remains
      file-backed.
- [ ] Run focused tests first, then `make fmt vet test race build` for every
      simplification change. Run `make smoke` when Docker is available.

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
  procedural-skill store under `~/.hermes/skills/`. Eggy now nudges
  proactively too, but on its existing heartbeat cadence rather than a new
  subsystem: `[x]` heartbeat turns see recent conversation and may call the
  same explicit, narrow agent tool calls a direct conversation turn can
  (`user_read`, `user_append`, `user_replace_section`, `user_remove_section`,
  and the `memory_*` equivalents) to curate silently, with or without also
  sending a check-in. Eggy still has no autonomous skill-creation loop (see
  "Separate durable facts from reusable procedures" below for Eggy's
  intentionally smaller take on that idea).
- Known gap: heartbeat curation currently shares the check-in's quiet-hours
  and weekly-message gate (`HeartbeatPolicy.CanSend`), so once that gate
  blocks a check-in (quiet hours, or the weekly proactive-message cap already
  hit), curation is skipped for the same window even though it sends no
  message. Splitting curation onto its own gate is a candidate follow-up if
  this proves too conservative in practice.
- Hermes layers in dialectic user modeling (Honcho) for a deepening
  cross-session user model. Eggy keeps `USER.md` a flat, agent-curated fact
  list.
- Eggy rejects likely secrets (passwords, tokens, keys) before any memory
  write reaches disk; this guard was not confirmed in Hermes's public docs.

The gap is intentional: Eggy stays file-backed, single-volume, and
provider-neutral rather than adding a database, search index, or skills
runtime purely to match Hermes.

- [x] Show a live capacity indicator (`[N% - used/max bytes]`) alongside
      `USER.md` and `MEMORY.md` in the system prompt, mirroring Hermes's
      "[67% — 1,474/2,200 chars]" snapshot annotation, so the model can see
      how full a document is before it writes.
- [ ] Set explicit, separate injected-size budgets for `USER.md` and
      `MEMORY.md` (today they share one `ContextStore`-wide byte cap with
      `SOUL.md` and custom prompts, unlike Hermes's distinct per-file caps).
- [ ] Keep `USER.md` for stable owner preferences and communication style.
- [ ] Keep `MEMORY.md` for compact durable facts, decisions, and reusable lessons.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and invisible
      Unicode content before memory writes.
- [x] Return a clear capacity error when curated memory is full instead of
      silently truncating or dropping stored content.
- [x] Give USER.md/MEMORY.md real CRUD instead of create/append/replace only:
      `user_read`/`memory_read` return the live on-disk document (so a
      curation pass isn't stuck reasoning from the stale turn-start prompt
      snapshot after it just wrote something), and
      `user_remove_section`/`memory_remove_section` delete a section outright
      (`ContextStore.RemoveSection`) instead of only ever being able to
      overwrite it. Heartbeat curation and direct conversation turns both
      have all four tools now.
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
