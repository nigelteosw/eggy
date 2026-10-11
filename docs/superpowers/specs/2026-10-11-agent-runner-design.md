# Optional Agent Runner plugin

Date: 2026-10-12 (rewrite of the 2026-10-11 draft)
Status: Proposed specification for user review; no implementation approved or delivered.

## Intent

An Eggy user asks Eggy to change one of the **repositories the deployment
allows**. Eggy itself is the coding agent: its own loop, running a cheap
OpenRouter model, edits a local checkout, runs the project's checks in it, and
Eggy's trusted publisher pushes a feature branch and opens a pull request. The
user gets the PR link. Eggy never approves, merges, enables auto-merge, or
touches a default or protected branch.

The earlier draft drove Claude Code with each user's subscription token. That is
dropped: it needed a second credential, a pinned CLI, a Superpowers adaptation
for a second agent, and an open question about running subscription tokens in a
hosted service. Using Eggy's own loop and the existing `openai_compatible`
provider path needs none of it, and keeps spend inside a budget the owner
controls through OpenRouter.

Decided with the owner:

- Eggy is the agent; no Claude Code, no subscription token.
- Only repositories in `config.yaml`'s `repositories:` are eligible. No
  arbitrary URLs, no forks.
- It appears on the Plugins page like Finance: one `agent_runner.enabled`
  switch, a settings card, a panel section.
- Telegram can also enable and disable cloning of an allowed repository, and
  start runs, with explicit commands.
- Cost is bounded by model choice and a per-run cap, not by a subscription.

Everything else below is a proposed default.

## Existing foundation

- `internal/plugins/finance` is the plugin pattern: one service, a tool and the
  panel as thin callers, a small panel-side interface, wired only in bootstrap.
- `config.Repositories` (`name`, `clone_url`, `base_branch`,
  `protected_branches`) is already the allowlist. `internal/repository/github`
  clones and reads with a deployment-wide token; `services/repo` holds
  read-only, conversation-bound checkouts. Coding runs need their own
  ownership and lifecycle; reuse the clone primitives, not the read-only thread
  bindings.
- `internal/subprocess/localprocess` provides argv execution, environment
  allowlisting, time and output limits and process-group kill. Its working
  directory check is **not** filesystem isolation (see Isolation).
- `internal/auth/grants` holds sealed SQLite records. `auth/connections` is
  keyed by shared connection, not by account, so it cannot hold a personal PAT
  unchanged.
- The approval service (`approvals.Action`, one executor and one payload-bound
  record per operation) stays the only authority for protected mutations.
- `internal/skills/store.go` loads bounded flat Markdown skills.

Whether per-account "which repositories are enabled" belongs in the existing
per-account state (`initializeAccountState` already seeds repositories per
account) or in one small table is the first thing the implementation plan
confirms against the code.

## User flow

1. Owner sets `agent_runner.enabled: true` from the Plugins page (settings
   card, existing config authority, normal restart semantics). Default false.
2. Each account connects its own GitHub fine-grained PAT in private web
   settings (Contents and Pull requests read/write, selected repositories).
   Eggy verifies identity and per-repository access, shows status, and offers
   replace and disconnect. Saved token values never return to the browser.
3. The account **enables cloning** for the allowed repositories it wants, in the
   panel or in Telegram (`/code repos`). Enabling is an explicit per-account
   switch on a repository the owner already allowed; it clones nothing until a
   run needs it. Nothing enables a repository on a person's behalf.
4. The account starts a run: the panel's Start form, or `/code owner/repo <task>`
   in a verified private Telegram chat. Ordinary chat may prepare a brief and
   link to a prefilled form but cannot start the run. Model output, tool
   arguments, repository text and generic selection replies never mint
   authorization.
5. The start copy states: "This task may create commits, push its feature
   branch, and open a pull request. Eggy will not approve or merge it. Your
   repository's CI may run on the branch."
6. A coding turn runs in the background. Normal chat stays responsive. Progress,
   questions, check results, a cancel control and the final PR link appear in
   the panel and in the originating channel.
7. The trusted publisher validates the result, pushes the run branch, opens the
   PR, and the link is stored and delivered.

## The agent

- **One loop.** A run is a turn on Eggy's existing agent loop with a coding
  tool set and a configured model, executed by the plugin's single worker. No
  second harness, no delegation, no subagents. This is the "scheduled turn, not
  a child loop" answer the declined-capabilities section already gives.
- **Model.** The coding turn uses an ordinary `providers` entry (OpenRouter is
  `openai_compatible` with the right `base_url`) selected through the existing
  per-account `/model` choice mechanism. No new adapter.
- **Coding tools** are `TurnScoped` to the coding turn and absent from owner
  turns: list/read/search files, edit (exact-replace and create), and a bounded
  shell. All are confined to the run's workspace directory. Their schemas cost
  nothing outside a coding turn.
- **One owner-facing tool, `agent_runner`:** start (requires the trusted
  binding below), status, list, cancel, answer. Reads declare `ReadOnlyTool()`;
  everything else stays a mutation. Unprompted turns (heartbeat, schedules)
  exclude the whole capability.
- **Superpowers.** Eggy loads a reviewed, namespaced Markdown subset through the
  existing skill mechanism: `using-superpowers`, `brainstorming`,
  `writing-plans`, and a trimmed `verification-before-completion`. TDD and
  executing-plans assume subagents and richer tooling; they are adapted to
  Eggy's sequential single loop or left out. Pin one reviewed revision,
  preserve licensing, no marketplace, no auto-update. The skills are exposed
  only while the plugin is enabled and the account's connection is ready;
  disabling removes the index and its prompt bytes.
- **Budget guard.** Each run has a hard cap on model tokens and wall time
  (constants, not config). Hitting it stops the run as `failed` with the partial
  work retained, never silently continues.
- **Questions.** A material ambiguity returns a structured question to the
  owning account (`waiting_for_user`). Answering resumes the turn from the
  saved brief and workspace; there are no indefinite sessions. A stale or
  foreign answer is rejected.

## Workspace and isolation

- Each run gets a fresh checkout under the persistent volume, for example
  `<root>/runs/<run-id>/`, on branch `eggy/<run-id>`. Paths come from server
  IDs, never user strings. The volume root is never exposed to the agent's
  tools.
- Clone and publish are **trusted adapter operations**. The PAT is used only by
  those, via an ephemeral credential mechanism, and never appears in argv, Git
  URLs or config, the agent's environment, prompts, traces or logs. Active
  secret filtering extends to account-held tokens through `Secrets.Values()`'s
  existing path; no second credential list.
- The shell tool runs through `localprocess` with a scrubbed environment (no
  Eggy secrets, no ambient Git config or credential helpers), the working
  directory pinned to the run workspace, the timeout, output cap and
  process-group kill it already enforces, and Git hooks disabled.

**What v1 does not protect against, stated plainly.** Everything runs as Eggy's
OS user. A prompt-injected file in an allowed repository could steer the model
into reading `/data/eggy.db`, the encryption key or other runs' workspaces. The
repositories are the owner's own (the existing trust model: configured
repositories are trusted code), which narrows but does not remove this.
Mitigations that cost almost nothing and ship in v1: allowlisted repositories
only, scrubbed env, workspace-pinned tools that reject paths resolving outside
the workspace (including through symlinks), the shell as the only escape hatch
and the model told nothing about Eggy's layout.

**Phase 2 (separate spec, not blocked on by v1):** an enforced boundary around
the shell — Linux namespaces or a separate UID with a network egress policy —
gated on a proof that Railway permits it (unprivileged user namespaces are not
guaranteed in the container). If that proof fails the plugin stays on the v1
trust model; it never claims isolation it cannot demonstrate. Until phase 2, the
Plugins card and docs say "runs commands as Eggy's user in repositories you
trust."

Limits as constants: one active run deployment-wide, one queued per account,
30 minutes, 2 GiB working data, 2 MiB persisted redacted output. Admission
checks free disk before clone. Interrupted or failed workspaces are kept seven
days; successful ones are removed after confirmed publication. Cleanup runs at
startup and at admission/completion; there is no new timer.

## Authorization

This deliberately changes the current ban on repository shipping and AGENTS.md
must be revised in the implementation, not before.

- **Run mandate.** Starting a run at a trusted ingress (verified web session,
  or a verified private Telegram sender through `/code`) creates one bounded
  mandate binding: account, ingress and request identity, brief digest,
  repository, base branch and SHA, the generated branch, GitHub credential
  generation, permitted operations (push that branch, open one PR) and a 24
  hour expiry. Expiry needs a new explicit request. The mandate is stored as a
  digest/reference; no bearer token is exposed to the model, and the dispatcher
  supplies the binding through context.
- **Two payload-bound approvals, derived from the mandate once the exact
  payloads exist:** push (exact commit and ref) and PR creation (repository,
  head, base, title, body). Each has its own `approvals.Action`, executor,
  digest and one-use claim, and each re-validates mandate, ownership and
  credential generation. Local commits need none; they have no remote effect.
  No general "approve on behalf of the user" API is added.
- This is a documented narrow exception to per-tool prompting, including
  `strict`: an explicit run request already authorizes this bounded workflow,
  and the diff is not reviewed by a human before the push. The start copy and
  docs say so. `normal`, `strict` and `auto` are unchanged for every other tool;
  `auto` cannot create a run without ingress authorization or widen a mandate;
  nothing is marked `InternalTool()` to get here.
- Denied in v1: default or protected branch writes, force-push, tags, deleting
  remote refs, review approval, merge, auto-merge, workflow-file changes (they
  would execute newly authored privileged CI), changes outside the allowed
  repository. Protection is checked live before push; if the check is
  unavailable the push fails closed. No heartbeat, schedule, MCP call,
  repository text or the agent's own tools can create or widen a mandate.

## Validation and publication

- After the coding turn ends and its process group is dead, the publisher
  computes the change set against the recorded base, rejects special files,
  escaping paths and secrets, preserves modes, deletions and binaries, and
  applies it to a fresh checkout with controlled Git metadata before committing.
  It never runs Git inside the agent's workspace against its hooks or config.
- Commit identity comes from the verified GitHub identity (no-reply email),
  never from model text.
- No changes means a completed "no change" result and no empty PR. Checks that
  failed or could not run produce a **draft** PR with a conspicuous summary;
  passing checks produce a regular PR. The PR body has the task summary,
  changes, the checks actually run and their results, limitations, and a stable
  run marker.
- **Check results are evidence only if Eggy ran them.** A model's claim that
  tests pass is not recorded as a result; the shell tool's captured exit codes
  are. Repository CI on the PR is the independent second check.
- Persist intent before each external effect. On timeout or restart, inspect the
  remote branch SHA and the PR's head, base and run marker before retrying. An
  existing branch with unexpected content stops the run rather than overwriting
  it. If push succeeded and PR creation failed, retry only the PR. Never
  duplicate a PR after an ambiguous response.

## Telegram surface

Telegram stays private chats only, sender-allowlisted, and a **direct command
surface**; selections cannot authorize anything.

- `/code repos` lists the allowed repositories with this account's clone
  status; `/code repos enable <name>` and `disable <name>` toggle it. Disabling
  refuses new runs and removes the account's idle workspace for that
  repository; a running job is cancelled first.
- `/code <name> <task>` starts a run; `/code status`, `/code cancel [id]`,
  `/code answer <id> <text>` manage it. Final PR links go to the originating
  verified destination. Discord receives the link as a notice only and cannot
  start, answer or enable anything in v1. Delivery uses the existing channel
  routing; a failed delivery is retried without republishing and stays visible
  in the panel.
- The commands live in `internal/commands` and call the same service as the
  panel; Telegram is a view, not a second implementation.

## Persistence and lifecycle

- One additive `agent_runs` table (`CREATE TABLE IF NOT EXISTS` with the other
  schemas, no `MachineStateVersion` bump): account and run identity, repository,
  mandate reference and expiry, state and revision, credential generation, base
  and head SHAs, pending question, bounded redacted result, publication stage
  and idempotency data, PR URL, notification status. The sealed GitHub token
  uses existing `auth_records` with associated data binding account, plugin,
  provider and generation. Account derives only from `ports.Principal`;
  everything fails closed without it.
- States: queued, running, waiting_for_user, publishing, completed, failed,
  cancelled, interrupted. Transitions are conditional transactional updates
  owned by the plugin service. Publication stage is stored separately so
  recovery never repeats a confirmed effect.
- One enabled-only worker, dispatched from the existing bootstrap lifecycle. A
  run is independent of the initiating HTTP connection. On restart the
  in-flight turn keeps the existing drain; coding processes get a bounded
  shutdown and become `interrupted`. Startup reconciles publication and queued
  work and does not relaunch an interrupted turn on its own. An owner can
  resume with the saved brief and workspace if the mandate is still valid, or
  must start again.
- Replacing or disconnecting the GitHub token advances its generation and
  invalidates queued and active authority; active coding stops and publication
  is refused. A request already sent to GitHub may complete, and the run is
  reconciled and reported, not rolled back. Cancellation, account removal and
  plugin disable prohibit new effects; none deletes an already-pushed branch
  or PR.

## Boundaries and wiring

| Area | Responsibility |
| --- | --- |
| `internal/plugins/agent_runner` | Service, transitions, `agent_runner` tool, coding tools, reviewed skill assets |
| `internal/ports` | Narrow run-store and publication contracts |
| `internal/storage/sqlite` | `agent_runs` table, claims, recovery state |
| `internal/auth` | Account-scoped sealed token access over existing grants primitives |
| `internal/repository/github` | Extend the existing client for scoped clone, push and PR; read-only callers stay compatible |
| `internal/core/services` | Approval service accepts the mandate-derived push and PR actions |
| `internal/commands` | `/code` commands |
| `internal/panel`, `website` | Plugins-page card and run UI; routes mounted only when the service is non-nil; capability listed in authenticated `/api/session` `features`, never `/api/mode` |
| `internal/config`, `internal/bootstrap` | `agent_runner.enabled`, construction when enabled and nil otherwise, worker and registration |

The panel declares its own small service interface and never imports the plugin.
There is no shared plugin interface. This is the second plugin; if it needs
shared structure Finance lacked, that is argued in the implementation plan, not
assumed. A disabled plugin has no tool schema, no route, no worker, no skills
and no prompt bytes. The enable card follows the rule that a new config section
is not done until it has a settings card, and nothing operates on the YAML file
by gesture.

## Footprint and deletion budget

New capability, not a reduction. Added: one startup key, one owner-facing tool
plus turn-scoped coding tools, one table, one sealed record per connected
account, per-operation approval records, one worker only while enabled. My
estimate is 2,500–4,000 handwritten production lines across Go and UI,
excluding tests and the pinned skills — noticeably smaller than the Claude Code
design because there is no CLI adapter, second credential or isolation layer in
v1. This is an estimate to hold the implementation to, not a target to hit by
deleting capability. Exceeding it requires saying what capability or security
need forced it. Nothing existing is deleted. Removing the plugin removes its
package, wiring and UI; the additive table may stay inert for rollback.

## Verification

Test-first; focused checks, then `make fmt vet test race build`; `make smoke`
with Docker or report the environment blocker.

1. Disabled plugin: no routes, tools, skills, worker or capability leakage.
2. Two accounts: guessed run IDs, tokens, logs, answers, workspaces, stale
   generations, concurrent disconnect and enable/disable races all fail closed.
3. Only a verified web start or `/code` from a verified private sender creates a
   mandate; model arguments, repository text, selections, schedules and
   heartbeats cannot. Modes behave as before outside the exception.
4. Push and PR each reject altered payloads, changed account/remote/ref, expiry,
   reuse, default/protected refs, merge, approval and force-push.
5. Path and symlink escapes from the coding tools are rejected; the shell
   environment contains no Eggy secrets; the token never appears in argv, Git
   config, logs or the workspace.
6. Crash injection around push and PR proves reconciliation, no duplicate PR, no
   lost URL, and notification retry without republishing.
7. Fake-model tests cover question, budget cap, cancellation, malformed tool
   calls and missing toolchain; check results come only from captured exit codes.
8. End to end on a real allowed repository with a real PAT and OpenRouter model:
   enable from Telegram, run, observe a feature-branch PR for the right account,
   private link delivery, and no approval or merge. Responsive browser coverage
   for setup, queued, running, question, error and PR states.

## Sequence for the implementation plan

1. Confirm per-account repository enablement storage; account-scoped sealed PAT;
   plugin shell and Plugins card.
2. Clone and workspace lifecycle; confined coding tools; run with a fake model.
3. Mandate, approvals extension, publisher, reconciliation.
4. `/code` Telegram commands and delivery.
5. Panel UI, skills subset, real-model end-to-end run.
6. Phase 2 (separate spec): enforced shell isolation, gated on a Railway proof.

## Review focus

The v1 trust model (same OS user, owner's own repositories, documented gap), the
mandate exception to per-tool prompting, the `/code` ingress, draft PRs when
checks fail or cannot run, and the constants for limits.
