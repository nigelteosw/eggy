# Eggy roadmap

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; durable design rationale belongs in `docs/adr/`;
completed implementation history remains in git.

Priorities are ordered by urgency, then by dependency. Check an item only when
its implementation and focused tests have landed.

## P0: Finish the architecture simplification

### Simplify shipping authorization

- [ ] Replace the three approval roles injected into `ShippingService` with one
      narrow authorization dependency that issues, automatically decides, and
      later authorizes an exact action and payload.
- [ ] Inject every shipping dependency through the constructor and remove
      setter-based partial construction.
- [ ] Preserve separate authorization records and checks for commit, push, and
      pull-request creation, including expiry, action matching, full-diff digest
      binding, branch/head checks, remote-head checks, and protected-branch
      denial.
- [ ] Invalidate obsolete pending shipping approvals at startup or migrate them,
      then remove shipping from Telegram's callback executor map and delete its
      compatibility-only cleanup paths.
- [ ] Keep explicit Telegram approval callbacks for repository registration and
      Calendar mutations. Pull-request merging remains unsupported.
- [ ] Add an end-to-end test proving the implementation loop cannot bypass any
      commit, push, or pull-request authorization check.

### Deployment follow-up

- [ ] Reset Railway's deployed `/data/config.yaml` so the next boot generates
      the current unversioned config shape. This is a manual deployment step.

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
- [ ] Design bounded, file-backed conversation search before implementing it;
      do not add a database solely for transcript recall.
- [ ] Keep recalled excerpts bounded, redacted, and explicitly marked as stale
      historical context rather than current authority.

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
