# Optional Agent Runner plugin

Date: 2026-10-11
Status: Proposed specification for user review; no implementation approved or delivered.

## Intent and agreed product behavior

An Eggy user connects their own GitHub personal access token and Claude Code
subscription token, asks Eggy to work on a repository, and receives a link to a
feature-branch pull request. Execution happens on the Railway deployment hosting
Eggy. The initial coding request authorizes implementation, committing to the
run's branch, pushing that branch, and opening its PR. There is no additional
publication confirmation. Eggy never approves, merges, enables auto-merge, or
bypasses protection on a PR.

Agent Runner is an optional compiled feature plugin. Credentials, runs, logs,
questions, and workspaces belong to the requesting account. Each user supplies
both credentials; no deployment-wide or other-account credential fallback is
allowed. Token entry in private web settings is the agreed first connection flow.
Both Eggy and Claude Code use Superpowers, with different responsibilities.

The remaining choices below are proposed defaults, not additional user decisions.
The Railway isolation proof is a prerequisite, not an assertion of feasibility.

## Existing foundation and approach

The inspected checkout already has:

- `internal/plugins/finance`: the optional-plugin pattern, with one service and
  thin panel/tool callers, wired only in bootstrap.
- `internal/repository/github`: Git clone and read-only GitHub operations using
  a deployment-wide token. Extend this adapter's capabilities rather than build
  another GitHub client; keep its existing read-only callers compatible.
- `internal/core/services/repo/workspace_sessions.go`: conversation-bound
  read-only checkouts. Coding runs have a separate ownership/lifecycle requirement;
  reuse checkout primitives, not these read-only thread bindings.
- `internal/subprocess/localprocess`: argv execution, environment filtering,
  time/output limits, and process-group termination. Its working-directory check
  is not filesystem or process isolation.
- `internal/auth/grants`: sealed records backed by SQLite. The existing
  `auth/connections` store is keyed by shared connection, not user; it cannot be
  reused unchanged for private tokens.
- `internal/core/approvals`, `services/approval_service.go`, and
  `services/approval_gate.go`: account-owned, payload-bound authorization and
  execution. Preserve this authority when adding the scoped run authorization.
- `internal/skills/store.go`: bounded flat Markdown skills, not Claude's plugin
  loader. Superpowers requires a deliberate Eggy adaptation.

Three approaches were considered:

| Approach | Consequence | Decision |
| --- | --- | --- |
| Run Claude directly as Eggy's OS user | Smallest change, but coding commands can reach Eggy's data and secrets | Reject |
| Isolate each run inside the existing Railway service | Preserves the agreed deployment and single supervisor | Select, subject to a deployment proof |
| Separate runner service or external sandbox provider | Stronger infrastructure separation, but changes deployment scope and introduces another service | Fallback proposal only if the selected approach fails; requires a revised spec |

## User flow

1. Enable `agent_runner.enabled` through the existing configuration authority.
   Default is false; normal restart semantics apply.
2. Open Agent Runner from the existing responsive web navigation. Connect a
   GitHub token and a token generated with `claude setup-token`. Show connection
   status, verified GitHub identity, and replace/disconnect actions; never return
   saved token values to the browser.
3. Select a repository using `owner/repo` or a canonical GitHub HTTPS URL and
   provide a task. Resolve it to GitHub's immutable repository ID, default/base
   branch, and base commit; check the user's access before starting.
4. Eggy uses Superpowers to clarify missing requirements and produce an actionable
   brief. A sufficiently concrete request can start directly. Material design
   questions still go to the user; authorization to publish is not permission to
   invent product requirements.
5. A trusted ingress binds a run request to the account, conversation, request
   text, selected repository, and generated feature branch. The UI states:
   “This task may create commits, push its feature branch, and open a pull request.
   Eggy will not approve or merge it.” Chat uses an explicit `/code owner/repo
   <task>` entry point for unambiguous first-version authorization; ordinary chat
   can prepare the brief and link to a prefilled Start run form. Model output or
   generic selection replies cannot mint authorization.
6. Eggy acknowledges the run, keeps normal chat responsive, and displays its
   progress, questions, test results, cancellation control, and final PR link.
7. Claude edits and runs checks inside its isolated workspace. Eggy's trusted
   publisher freezes and validates the result, commits, pushes, and creates the PR.
   It sends the link to the original verified channel and stores it in run history.

The explicit chat command and form are proposed ingress choices to make initial
authorization verifiable without a second confirmation. They do not authorize
arbitrary shell execution or repository operations outside this plugin.

## Credentials and ownership

Store both tokens sealed in existing `auth_records`, through an account-aware
credential service using the existing sealer. Bind ciphertext associated data to
account ID, plugin name, provider, and credential generation. Derive the account
only from `ports.Principal`; fail closed without it. Reads and mutations of run
records, artifacts, questions, logs, and credentials all enforce ownership.

GitHub: recommend a fine-grained PAT restricted to selected repositories, with
Contents read/write and Pull requests read/write. Validate identity/access without
creating a branch during setup; recheck repository access at run admission and
publication. Organization approval and token limitations must appear as actionable
connection errors. First version supports github.com and repositories where the
user can push a branch; no forks, enterprise hosts, SSH keys, or arbitrary URLs.

Claude: pass the stored setup token only to the official Claude Code process as
`CLAUDE_CODE_OAUTH_TOKEN`. Use an isolated per-run configuration directory. Do not
use `--bare`, inherit API billing variables, copy local login files, or send this
token through Eggy's LLM adapter. A user-triggered connection test may consume a
small amount of subscription usage and must say so. Saved is distinct from tested
and ready. Expiry/quota failure pauses or fails the run without API-key fallback.

Replacing or disconnecting either credential advances its generation and
invalidates queued/active authority using the old generation. Stop active coding
and refuse subsequent publication. An already-issued remote request may complete;
reconcile and report it rather than promising rollback. Disconnect removes Eggy's
copy; provider-side revocation is separate.

Extend the existing active-secret filtering/redaction path to include these dynamic
credentials without introducing a competing static credential list. Tokens must
not enter prompts, traces, command arguments, Git URLs/config, chat, PR text, or
plaintext durable files. The Claude process necessarily receives its own token;
do not promise that an arbitrary command inside that same run cannot observe it.
Other users' credentials and Eggy's environment must remain inaccessible.

## Authorization and deliberate policy changes

This feature explicitly changes the current ban on repository shipping and the
ban on coding-agent execution. It does not introduce general subagent routing, a
second Eggy harness, a generic shell tool, or a plugin marketplace.

Initial authorization is a bounded run mandate captured at a trusted ingress.
Its immutable scope contains account, ingress/request identity, brief digest,
repository ID and canonical remote, base ref/SHA, generated branch, credential
generations, permitted operations, and expiry. Proposed validity: 24 hours;
expiry requires a new explicit user request, not model renewal.

The final diff is unknown at admission. Do not misrepresent the mandate as a
human review of that diff. Extend the existing approval service to accept this
specific upfront authorization and derive separate payload-bound records for
commit, push, and PR creation once their exact payloads exist. Each has its own
`approvals.Action`, executor, digest, one-use execution claim, and independent
mandate/ownership/generation validation. Push binds the exact commit and ref;
PR creation binds repository, head, base, title, and body. No general public
“approve on behalf of user” API is added.

This is a documented, narrow exception to per-tool prompting, including strict
mode: an explicit Agent Runner request already authorizes this bounded workflow.
Normal/strict/auto remain unchanged for all other tools. Even auto cannot create
a run without ingress authorization or widen a mandate. Do not mark shipping as
`InternalTool()` or select auto mode for the user. The existing native gate must
recognize already-authorized runner operations through the same approval service;
it must not simply unwrap the tool or trust an argument claiming approval.

First-version operations are clone, isolated edits/checks, commit, push the new
run branch, and open one PR. Deny default/protected branch writes, force-push,
tags, deletion of remote refs, review approval, merge, and auto-merge. Check
protection live before push, permit only the server-generated branch, and fail
closed when the required protection check is unavailable. No heartbeat, schedule, MCP call, repo
instruction, or Claude subprocess may create a new mandate.

Implementation must revise the relevant AGENTS.md rules together, documenting
the exception and preserving all other approval rules. This spec does not edit
those rules ahead of implementation.

## Railway execution and isolation prerequisite

Keep one `eggyd` supervisor and one replica; Claude and build tools are bounded
child processes, not another application service. The first implementation
milestone must prove the exact runtime on Railway with non-secret fixtures.

Candidate: an outer Linux namespace sandbox around the entire CLI and all its
descendants, with a private mount/PID/network view, dropped privileges and
capabilities, resource limits, and an explicit filesystem allowlist. Bubblewrap
is a candidate, not a verified Railway capability. Claude's Bash-only sandbox
and separate home directories are not sufficient isolation for the whole CLI.

The proof must demonstrate:

- Reads/writes cannot reach `/data/eggy.db`, config, owner documents, host process
  environments, sibling runs, sockets, or the encryption key, including through
  symlinks, hard links, `/proc`, and inherited file descriptors.
- Only this run's workspace, scratch home, and approved read-only runtime/skills
  are mounted. No Docker socket or host-control capability is provided.
- Network policy denies Eggy's loopback/private service endpoints and metadata
  services; only required model and approved dependency endpoints are reachable.
  Redirect/DNS behavior cannot escape that policy. Git credentials never enter
  the coding sandbox; clone and publication are trusted adapter operations.
- Descendants cannot escape cancellation; CPU, memory, process count, disk, and
  wall time limits preserve Eggy's responsiveness.
- The pinned Claude version accepts subscription auth, loads Superpowers, can
  edit/run a representative test, and returns machine-readable results.

If these properties cannot be achieved in the Railway service, mark the plugin
unavailable with a diagnostic. Never fall back to the current localprocess runner,
weaker shared `/proc`, or unrestricted permission bypass. The spec then requires
revision for a separate isolation boundary; successful Docker image construction
alone is not deployment proof. No live Railway proof was performed for this spec.

Proposed initial limits: one active run deployment-wide, at most one queued run
per account, 30 minutes execution time, 2 GiB working data per run, 2 GiB process
memory, 2 CPUs, 128 processes, and 2 MiB persisted redacted output. Admission checks
available resources and disk before clone. Missing enforceable limits fail the
readiness check. Keep these defaults as constants initially, not new config knobs.

Use the persistent volume for controlled checkout/artifact directories, never
mount its root inside a run. Machine state remains in SQLite. Git working files
are execution artifacts; scratch Claude JSON/config/history is temporary and is
deleted after execution, not a fourth durable state store. Retain interrupted or
failed workspaces for seven days; clean successful workspaces after confirmed
publication. Cleanup runs at startup and admission/completion, not in a new timer.

## Superpowers and the coding protocol

Pin and review one Superpowers revision and one Claude Code version in the build;
record both in each run. Preserve upstream licensing. No runtime marketplace
installation or automatic skill updates.

Eggy loads an adapted, namespaced Markdown subset through its existing skill
mechanism: using-superpowers, brainstorming, writing-plans, and
verification-before-completion. Resolve references to bundled Markdown explicitly;
do not claim Eggy supports upstream hooks/scripts or Claude-specific tools.
Expose this subset only when the plugin and the account's connections are ready;
disabling the plugin removes its skill index and prompt bytes.

Claude receives the reviewed plugin through its native plugin mechanism, mounted
read-only. Its task uses the sequential executing-plans/TDD/debugging/verification
workflow. Disable subagent delegation for v1. Eggy prepares and clarifies the
brief; Claude implements it, rather than repeating the same planning dialogue.
The task handoff explicitly states which design is approved and which autonomous
steps the user authorized. Material ambiguities return a structured question to
Eggy; no fake approvals or indefinite waits on an unattended terminal.

Use the official CLI programmatic mode and bounded structured output, with fixed
argv and prompts through stdin. Configure explicit tool permissions; do not expose
Eggy's tools, MCP servers, credentials, or configuration. Repository instructions
can guide implementation but cannot modify the launcher policy, activate hooks,
load unreviewed plugins/MCP, change the remote, or gain publication authority.
CLI settings and plugin precedence must be verified against the pinned release.

Results distinguish completion, question, tests failed, missing toolchain, quota,
auth, timeout, and cancellation. Tool output is untrusted. A success message from
Claude alone is not evidence of a valid diff or passing tests.

## Validation and publication

Use a new checkout and `eggy/<run-id>` branch per run; resolve paths from server
IDs rather than user strings. Reject local/file/SSH remotes, embedded credentials,
URL redirects to other hosts, and unsafe refs. Disable recursive submodules and
Git hooks; do not inherit system/user Git configuration or credential helpers.

After stopping all coding descendants, the trusted publisher verifies the base,
branch, remote identity, and changed paths. It must not execute Git against
attacker-controlled hooks/config. Build the publishable tree in a fresh trusted
checkout with controlled Git metadata, then rerun required checks in a separate
credential-free sandbox. Preserve legitimate modes/deletions/binary files while
rejecting special files, escaping paths, and secrets. Changes to GitHub workflows
are outside v1 and fail with an explanation; this avoids silently expanding PAT
permissions or executing newly authored privileged CI.

The minimum runtime includes Git, the pinned CLI, and the existing Go toolchain;
add Node/Bun only as documented, pinned build dependencies for supported projects.
Do not claim every repository can build. Report unavailable tools and checks
honestly. No changes yields a completed no-change result without an empty PR.
Failed or unavailable checks produce a draft PR with a conspicuous test summary;
passing required checks produce a regular PR. Neither result approves or merges it.

The trusted adapter performs commit/push/PR operations using only this account's
credential. Commit identity comes from the verified GitHub identity with an
appropriate no-reply email, not model text. PR description includes task summary,
changes, actual tests/results, limitations, and the stable run marker. Publication
may trigger the repository's existing CI; users must see this in the start copy.

Persist intent before each external side effect. On a timeout, inspect the remote
branch SHA and PR head/base/run marker before retrying. If a branch exists with
unexpected content, stop rather than overwrite. If push succeeded but PR creation
failed, retain the branch and retry only PR creation. Never duplicate a PR after
an ambiguous response or deployment restart.

## Lifecycle, persistence, and integration boundaries

Proposed durable states: queued, preparing, running, waiting_for_user, validating,
publishing, completed, failed, cancelled, interrupted. Store publication stage
separately so recovery does not repeat confirmed effects. Questions have an ID and
brief revision; only the owning account can answer, and stale answers are rejected.
A changed repository or materially expanded task requires a new mandate.

Create one additive `agent_runs` table holding account/run identity, immutable
request and mandate reference, state/revision, credential generations, destination,
base/head SHAs, question, bounded redacted results, stage/idempotency metadata,
PR URL, and notification status. Reuse auth_records and approval records rather
than duplicate their contents. All state transitions use conditional transactional
updates. No MachineStateVersion bump for this additive schema.

The plugin service owns transitions. Bootstrap dispatches queued work from the
existing lifecycle, with one enabled-only execution worker; there is no second
agent loop or scheduler. A submitted run is independent of the initiating HTTP
connection. Normal Eggy restart keeps existing turn draining; coding processes
receive a bounded shutdown and become interrupted. Startup reconciles publication
and queued work; it does not blindly relaunch interrupted Claude execution.
Owner-triggered resume uses the saved brief/workspace and current valid mandate,
or requires a new request if expired or invalidated. No indefinite CLI sessions.

Notification delivery uses the existing destination/channel routing. Retry delivery
without repeating publication. Persist the final PR URL before notification;
delivery failure remains visible in the panel. Account removal, credential
disconnect, cancellation, and plugin disable prohibit new effects. Cancellation
does not delete an already-pushed branch or PR.

Responsibilities:

| Area | Responsibility |
| --- | --- |
| `internal/plugins/agent_runner` | Service, state transitions, tool, reviewed skill assets |
| `internal/ports` | Narrow provider-neutral run storage, coding execution, and publication contracts |
| `internal/storage/sqlite` | Owned run records, transactional claims and recovery state |
| `internal/auth` | Account-scoped sealed credential access using existing grants primitives |
| `internal/repository/github` | Extend existing transport for scoped clone, push, and PR operations |
| `internal/subprocess` | Enforced isolation and bounded child lifecycle |
| `internal/coding/claude` | Claude-specific argv, auth injection, output parsing, and plugin loading |
| `internal/core/services` | Existing approval authority extended for narrowly scoped upfront authorization |
| `internal/panel` and `website` | Private connection/run UI, start/cancel/answer/status endpoints |
| `internal/commands` | Explicit coding-request ingress for verified private chat |
| `internal/config`, `internal/bootstrap` | Enable flag, construction, registration, lifecycle wiring |

Panel declares its own service interface and never imports the plugin. Add the
feature to authenticated `/api/session`, never `/api/mode`. One tool,
`agent_runner`, offers start/status/list/cancel/answer operations; start requires
the trusted mandate, never arbitrary command text as execution authority.
Persist only a digest/reference for the mandate; do not expose a bearer authority
token to the model. The dispatcher supplies the verified request binding in context.
Read actions declare their effect; mutations remain mutations. Unprompted tool
allowlists exclude the entire runner capability. Accounts without ready
connections see setup UI but pay no runner schema or skill prompt cost.

## Footprint and deletion budget

This is a new capability, not a line-reduction refactor. Proposed review budget:
at most 3,000 added handwritten production lines across Go and UI, excluding
tests and pinned upstream skills; one startup key (`agent_runner.enabled`), one
tool definition, one table, two sealed provider records per connected account,
existing approval records per operation, and one worker only while enabled.
No new scheduler, database, application service, generic lifecycle framework, or
runtime marketplace. Exceeding the estimate requires explaining the specific
capability/security need before expanding the plan.

Delete no existing capability. Extend the existing GitHub transport, sealers,
approval service, channel delivery, and subprocess primitives instead of creating
parallel implementations. Removing the plugin should remove its package, wiring,
UI, and adapters specific to coding; additive records may remain inert for rollback.
Runtime-disabled means zero tools/routes/workers/prompts, not zero image bytes.

## Acceptance and verification

Implementation proceeds test-first with focused checks, then `make fmt vet test
race build`; run `make smoke` with Docker or report the environment blocker.
Add responsive browser coverage for setup, queued/running/question/error/PR states.

Required evidence:

1. Disabled plugin has no routes, tools, skill index, worker, or private capability
   leakage; an unconnected account cannot invoke another account's runner.
2. Two-account tests cover guessed run IDs, credentials, logs, answers, artifacts,
   stale generations, concurrent disconnect, and background ownership restoration.
3. Explicit request creates the bounded authorization once; model arguments,
   repository text, generic Telegram selections, schedules, and heartbeats cannot.
   All modes retain their behavior outside this narrow exception.
4. Commit/push/PR independently reject altered payloads, account/remote/ref changes,
   expiry, reuse, protected/default refs, merge/approval, and force-push attempts.
5. Railway isolation proof covers every property above, including descendants,
   networking, resource exhaustion, and malicious Git metadata. No unsafe fallback.
6. A real opt-in run with each user's own subscription loads the pinned skills;
   fake subprocess tests cover malformed/truncated output, questions, expiry,
   quota, missing tools, and cancellation. No API billing fallback occurs.
7. Crash injection before/after push and PR calls proves reconciliation, no duplicate
   PR, no lost URL, and notification retry without republishing.
8. End-to-end: private repo clone, isolated change, reported checks, correct
   user's feature branch/PR, private link delivery, and no approval/merge.

The implementation plan should sequence: Railway/auth/skill feasibility proof;
owned credentials and plugin surface; run authority and persistence; isolated
Claude execution; trusted publication/recovery; integrated UI and end-to-end checks.
Each stage has an independently testable deliverable. Do not build the publication
UI around an unproven isolation mechanism.

## Sources and review status

Primary references checked while preparing this spec:

- [Claude authentication](https://code.claude.com/docs/en/authentication): setup
  tokens, subscription use, environment precedence, and the bare-mode limitation.
- [Programmatic Claude Code](https://code.claude.com/docs/en/headless): CLI execution
  and structured results; pin and test the version used by the adapter.
- [Claude sandboxing](https://code.claude.com/docs/en/sandboxing): Linux dependencies
  and nested-container limitations; does not establish Railway compatibility.
- [Superpowers](https://github.com/obra/superpowers): harness-specific installation
  and skill workflow; Eggy's adaptation is new work.
- [Railway volumes](https://docs.railway.com/volumes): persistent deployment storage.
- [GitHub PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens):
  repository-scoped tokens and organization restrictions.

Only this specification is delivered. Review should focus on the bounded upfront
authorization exception, explicit request ingress, draft PR behavior on failed
checks, resource defaults, and the fail-closed Railway feasibility prerequisite.
After written-spec approval, create the detailed implementation plan.
