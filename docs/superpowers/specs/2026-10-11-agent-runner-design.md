# Agent Runner extension

Date: 2026-10-12 (second revision of the 2026-10-11 draft)
Status: Approved 2026-10-12; implementation plan at `docs/superpowers/plans/2026-10-12-agent-runner.md`.

## Intent

An Eggy user asks Eggy to change one of the **repositories the deployment
allows**. A coding turn on Eggy's own loop, using the model the Agent Runner
page selects, edits a local checkout and runs the repository's configured
checks. The extension then pushes a feature branch and opens a pull request,
and the user gets the link. Eggy never approves, merges, enables auto-merge, or
touches a default or protected branch. Review and merge stay with a human.

Decided with the owner:

- Eggy's loop is the agent. No Claude Code, no subscription token.
- Only repositories in `config.yaml`'s `repositories:` are eligible. No
  arbitrary URLs, no forks.
- It is an **extension**, like Finance: `internal/extensions/agentrunner/`, one
  `agent_runner.enabled` switch, a card and a page under Extensions.
- The Agent Runner is a **separate control surface from Eggy's chat**: its own
  model setting, its own skill toggles, its own page.
- Each account clones and publishes with **its own GitHub PAT**, set in its
  private settings.
- The agent has a **sandboxed `bash` tool**: it runs as a separate
  unprivileged user (option A) under Landlock (option B). If the sandbox
  cannot be confirmed at startup, `bash` is not offered and runs fall back to
  `run_check` alone.
- Only the owner's configured `checks`, run by Eggy, count as evidence in the
  PR.
- Starting a run authorizes it to push its own branch and open one PR. There is
  no per-push prompt. Approval and merge are never automated.
- Telegram can enable repositories and start runs with explicit commands.

## Existing foundation

- `internal/extensions/finance` is the pattern: one service as the only write
  path, the tool and the panel as thin callers, a small panel-side interface,
  constructed in bootstrap only when enabled.
- `config.Repositories` (`name`, `clone_url`, `base_branch`,
  `protected_branches`) is the allowlist. This extension adds one optional
  field, `checks`.
- `internal/subprocess/localprocess` runs argv with environment allowlisting,
  time and output limits, and process-group kill.
- `internal/auth/grants` holds sealed SQLite records (`auth_records`).
- `internal/skills` loads bounded Markdown skills.
- `agent.NewSelectedLoop(models, source, policy)` builds a loop over any
  `agent.ToolSource`; the runner passes its own.

## Code organization

Everything the extension owns lives in one package tree. It is not spread
across adapters.

```
internal/extensions/agentrunner/
  service.go      run lifecycle, state transitions, the one write path
  tool.go         owner-facing `agent_runner` tool (thin caller)
  codetools.go    turn-scoped coding tools: list, read, search, edit, bash, run_check
  sandbox.go      coder uid, Landlock ruleset, sandbox-exec entry, startup probe
  workspace.go    run directories, path confinement, cleanup
  publish.go      change-set validation, commit, push, PR, reconciliation
  github.go       clone/push/PR over the account's PAT (narrow, extension-local)
  skills/         pinned, reviewed Superpowers subset (embedded)
```

Outside the package, it touches only these:

| Area | Change |
| --- | --- |
| `internal/ports` | Narrow `AgentRunStore` contract |
| `internal/storage/sqlite` | `agent_runs` table (additive) and the store |
| `internal/config` | `agent_runner` section (`enabled`, `model`, `skills`) and `repositories[].checks` |
| `internal/commands` | `/code` commands calling the service |
| `internal/panel` + `website` | Extensions card, Agent Runner page, PAT field in personal settings; own small interface, routes only when non-nil, `features` in authenticated `/api/session` |
| `internal/bootstrap` | Construct when enabled, register tools, start the worker |
| `cmd/eggyd` | Hidden `sandbox-exec` entry point that calls into the extension's `sandbox.go` |
| `Dockerfile` | `coder` user, `bash`, `git`, Go toolchain and Node LTS in the final stage |

`github.go` stays inside the extension rather than extending
`internal/repository/github`. That adapter serves read-only checkouts with the
deployment token, and the two credential paths must never share a checkout. If
a second caller ever needs push or PR, that is the moment to move it.

## Configuration surfaces

All settings go through `internal/config` and are edited from the Agent Runner
page. Nothing is edited by hand in YAML.

```yaml
agent_runner:
  enabled: false
  model: openrouter-cheap        # a `providers` alias; independent of /model
  skills:                        # Superpowers subset, each toggleable
    brainstorming: true
    writing-plans: true
    verification-before-completion: true
repositories:
  - name: eggy
    clone_url: https://github.com/owner/eggy.git
    checks:                      # argv lists; the only commands a run can execute
      - ["go", "vet", "./..."]
      - ["go", "test", "./..."]
```

- **Model.** Coding turns always use `agent_runner.model`. An account's chat
  `/model` choice has no effect on coding turns, and this setting has no effect
  on chat. The page lists the configured providers to choose from.
- **Skills.** The Agent Runner page lists its bundled skills with on/off
  toggles. Eggy's Customize → Skills page lists only Eggy's skills. The two
  sets never mix: the agent runner's skills appear only in coding turns, and
  Eggy's skills never appear in coding turns.
- **PAT.** Each account sets its fine-grained GitHub PAT (Contents and Pull
  requests read/write, selected repositories) in its private settings. Eggy
  checks the token's identity and access to each repository, shows the status,
  and offers replace and disconnect. The saved value never returns to the
  browser. It is sealed in `auth_records`, with associated data binding the
  account, the extension and a generation number.

## User flow

1. The owner turns on `agent_runner.enabled` from Extensions and picks a model.
2. Each account sets its PAT in settings.
3. The account enables the allowed repositories it wants, from the page or with
   `/code repos enable <name>`. Enabling clones nothing until a run needs it,
   and nothing enables a repository on someone's behalf.
4. The account starts a run from the page's Start form, or with
   `/code run <name> <task>` in a verified private Telegram chat. Ordinary chat
   can prepare a brief and link to a prefilled form, but cannot start a run.
5. The start copy says: "This run will push a feature branch and open a pull
   request on your behalf. Eggy will not approve or merge it. Your repository's
   CI will run on the branch."
6. The run happens in the background while chat stays responsive. Progress,
   questions, check results, a cancel control and the final PR link appear on
   the page and in the channel the run started from.

## The agent

- **One loop.** A run is a turn on Eggy's existing loop with the coding tool
  set, executed by the extension's single worker. No second harness and no
  subagents. This is the "scheduled turn, not a child loop" answer.
- **Coding tools:** `list`, `read`, `search`, `edit` (exact-replace and
  create), `bash`, and `run_check`. They are **not registered in Eggy's tool
  registry**: the runner builds the same `agent.Loop` with its own
  `agent.StaticTools` source holding only these six. Owner turns therefore
  never see them, and the `/mode` approval gate never wraps them, because the
  run's mandate and the sandbox are what authorize and contain them. The file
  tools are confined to the run workspace, and paths that resolve outside it,
  including through symlinks, are rejected.
- **`bash`** runs `bash -c <command>` in the workspace inside the sandbox,
  with a per-command timeout (5 minutes), an output cap, and process-group
  kill. It is how the agent explores, runs `git diff`, and installs
  dependencies. Its exit codes are never reported as check results.
- **`run_check`** takes an index into the repository's `checks` list and runs
  that argv in the same sandbox. Only these results appear in the PR as checks
  run. A repository with no `checks` can still be edited; its PR is opened as
  a draft that says no checks ran.
- **Owner-facing tool `agent_runner`:** status, list, cancel, answer. Status
  and list declare `ReadOnlyTool()`. **Start is not a tool action**: a run is
  started only from the page or from `/code run`. Unprompted turns exclude the
  whole tool.
- **Budget.** Each run has a hard cap on model tokens and on active wall time
  (constants). Hitting a cap ends the run as `failed` and keeps the workspace.
- **Questions.** A material ambiguity becomes a structured question to the
  owning account, and the run enters `waiting_for_user`. While waiting, the
  run **gives up the worker slot and its wall clock stops**. An answer puts it
  back in the queue with the time it had left. An unanswered question expires
  with the mandate (24h): the run ends as `cancelled` and the workspace is
  kept. Stale or foreign answers are rejected.

## Sandbox

Every command a run executes, from `bash` and from `run_check`, runs in the
same sandbox. Eggy's own process never runs repository code.

**Verified on Railway (2026-10-12)**, read-only probes in the production
container:

- kernel 6.12;
- `eggyd` runs as root with a default capability set and can switch a child to
  an unprivileged uid;
- `landlock_create_ruleset` reports **ABI 6** for both root and an
  unprivileged user, so file access, TCP connect and bind, and IPC scoping
  (abstract Unix sockets and signals) are all enforceable.

**A — separate user.** The image adds a `coder` user with no login and no
groups. Each run's directory `/data/agent-runs/<run-id>/` (workspace, `home/`,
`tmp/`) is owned by `coder`. At boot, `eggyd` tightens everything else under
`/data` to root-only (`0700` directories, `0600` files). `coder` therefore
cannot read `eggy.db`, the encryption key, sealed tokens or other runs, even
if Landlock were absent.

**B — Landlock.** Go cannot run code between fork and exec, so `eggyd`
re-executes itself through a hidden `eggyd sandbox-exec` entry point. That
entry point is started already as `coder` (`SysProcAttr.Credential`, new
process group). It sets `no_new_privs` and resource limits (processes, file
size, address space), then applies a ruleset before `exec`ing the command:

- read and execute: `/usr`, `/bin`, `/lib*`, `/etc`, plus toolchain
  directories the image provides;
- read and write: this run's workspace, `home/` and `tmp/` only;
- network: TCP connect only to ports 443 and 80 (fetching dependencies), no
  bind; Eggy's own listening port is unreachable;
- scoped abstract Unix sockets and signals, so the sandbox cannot signal or
  reach `eggyd`.

The environment is built from scratch: `PATH`, `HOME` and `TMPDIR` pointing
into the run directory, `LANG`, and nothing from Eggy's environment. There is
no Git credential helper and no Git config except a run-local one with hooks
disabled.

**Startup probe, fail closed.** When the extension starts, it runs the
sandbox once: `true` must succeed, and reading `/data/eggy.db`, writing
outside the run directory, and connecting to Eggy's port must all fail. If
any check fails, `bash` is not registered, runs use `run_check` (still as
`coder`, with whatever Landlock is available), and the Agent Runner page says
why. The tool never claims a sandbox it did not verify.

**What remains.** Outbound HTTPS is open, so anything in the workspace can
leave the machine. That is the repository's own code, which is already on
GitHub, and nothing else is readable. Code the agent writes also runs in your
CI on the branch. Both are **accepted for v1**. The Extensions card says:
"Runs commands as a sandboxed user that can reach the internet but not Eggy's
data."

**Toolchains: Go and Node, baked into the image.** The final stage already
copies `/usr/local/go` from the `golang:1.26-bookworm` builder and installs
`git` and `curl`; Debian slim already has `bash`. The image adds Node LTS from
the official binary tarball into `/usr/local/node` (SHA-256 verified) and puts
`/usr/local/node/bin` on `PATH`. Both toolchain directories are on the
sandbox's read-and-execute list. Module and package caches (`GOMODCACHE`,
`GOCACHE`, `npm_config_cache`) live in the run's `home/`, so dependencies are
downloaded per run over HTTPS and never shared between runs. Other languages
are added to the image when a repository needs them.

## Authorization

AGENTS.md's ban on repository shipping is revised **in the implementation**,
as a recorded, narrow exception.

- **The mandate.** Starting a run at a trusted ingress (a verified web
  session, or `/code run` from a verified private Telegram sender) creates one
  mandate. It binds the account, the ingress and request identity, the brief
  digest, the repository, the base branch and SHA, the generated branch
  `eggy/<run-id>`, the PAT generation, the permitted operations (push that
  branch and open one PR against the base), and a 24 hour expiry.
- **The mandate is the authorization.** Push and PR creation each re-validate
  it (account, generation, branch, expiry, one-use for the PR) immediately
  before the call. No `approvals.Action` is created, so nothing pretends a
  human approved the diff. Human review happens on the PR, which Eggy can
  never approve or merge. This applies in every `/mode`. The modes are
  unchanged for all other tools, and nothing is marked `InternalTool()`.
- **Never permitted:** writes to the default or a protected branch,
  force-push, tags, deleting refs, review approval, merge, auto-merge, and
  changes outside the repository. Protection is checked live before the push,
  and an unavailable check fails closed. No heartbeat, schedule, MCP call,
  model output, repository text or agent tool can create or widen a mandate.
- **`.github/**` is read-only for the agent.** A change set that touches it is
  rejected before commit. This covers workflows, composite actions,
  CODEOWNERS and dependabot. CI still runs the agent's edited code on the
  branch, which is the accepted risk above.

## Validation and publication

- After the coding turn ends, the publisher computes the change set against
  the recorded base. It rejects special files, escaping paths, `.github/**`
  and detected secrets, and preserves file modes, deletions and binaries. It
  applies the change set to a fresh checkout with controlled Git metadata and
  hooks disabled, then commits. It never runs Git with the workspace's config.
- The commit author is the PAT's verified GitHub identity, using its no-reply
  email.
- No changes means a "no change" result and no PR. Failed or missing checks
  produce a **draft** PR. Passing checks produce a regular PR. The PR body has
  the summary, the changes, the checks actually run with their exit codes,
  limitations, and a stable run marker.
- Check results come only from `run_check`'s captured exit codes, never from
  the model's claims.
- Publication stages are persisted before each external effect:
  `committed → push_intended → pushed → pr_intended → pr_opened → delivered`.
  On a restart or timeout, Eggy inspects the remote branch SHA and looks for
  the PR's run marker before retrying. A branch with unexpected content stops
  the run. If the push succeeded but PR creation failed, only the PR is
  retried, and an ambiguous response never leads to a duplicate PR.

## Telegram surface

`/code` is a direct command surface in private, allowlisted chats. Every
subcommand is a fixed word, so a repository name can never collide with one.

- `/code repos` lists allowed repositories with this account's status.
  `/code repos enable <name>` and `/code repos disable <name>` toggle one.
  Disabling cancels an active run on that repository, refuses new ones, and
  removes the idle workspace.
- `/code run <name> <task>`, `/code status`, `/code cancel [id]`,
  `/code answer <id> <text>`.
- Discord receives PR links as notices only. It cannot start, answer or
  enable anything.
- The commands live in `internal/commands` and call the same service as the
  panel.

## Persistence and lifecycle

- One additive `agent_runs` table (`CREATE TABLE IF NOT EXISTS`, no
  `MachineStateVersion` bump). It holds the account, repository, mandate,
  state and revision, PAT generation, base and head SHAs, pending question,
  remaining budget, bounded redacted output, publication stage, PR URL and
  delivery status. Per-account repository enablement is either a small table
  or existing account state; the plan confirms which against
  `initializeAccountState`.
- States: queued, running, waiting_for_user, publishing, completed, failed,
  cancelled, interrupted. Transitions are conditional updates owned by the
  service.
- The worker exists only while enabled. On restart, the in-flight turn drains
  as usual, check processes get a bounded shutdown and their runs become
  `interrupted`, publication is reconciled, and nothing relaunches on its own.
  An interrupted run can be resumed while its mandate is valid.
- Replacing or disconnecting the PAT advances its generation, which stops
  active work and refuses publication. Cancelling, removing an account or
  disabling the extension stops new effects but never deletes a pushed branch
  or an opened PR.

## Limits (constants)

- One active run across the deployment, and one queued run per account.
  Waiting runs hold no slot.
- 30 minutes of active wall time per run.
- 2 GiB of working data per run, 10 GiB for all retained workspaces. Admission
  refuses a run that would exceed either limit.
- 2 MiB of persisted redacted output.
- Failed, interrupted and cancelled workspaces are kept 7 days. Successful
  ones are deleted after publication is confirmed. Cleanup runs at startup and
  at admission and completion, with no new timer.

## Footprint and deletion budget

This is a new capability, not a reduction. It adds:

- three config keys plus `repositories[].checks`;
- one owner-facing tool and seven coding tools (including `ask_owner`), outside the registry;
- one `coder` user and a hidden `sandbox-exec` entry point;
- one table and one sealed record per connected account;
- one worker that runs only while the extension is enabled.

The estimate is 2,300–3,800 production lines across Go and UI, excluding
tests and the pinned skills. The sandbox adds about 300 lines. Going over the estimate requires naming the
capability or security need that forced it. Nothing existing is deleted.

## Verification

Test-first; focused checks, then `make fmt vet test race build`; `make smoke`
with Docker or report the environment blocker.

1. A disabled extension exposes no routes, tools, skills, worker, or
   `features` entry.
2. Agent Runner skills never appear in owner turns, Eggy skills never appear
   in coding turns, and the toggles take effect. Coding turns use
   `agent_runner.model` regardless of `/model`.
3. Across two accounts, guessed run IDs, answers, workspaces, PATs and stale
   generations all fail closed.
4. Only a page start or `/code run` creates a mandate. The `agent_runner` tool
   has no start action, and schedules and heartbeats cannot reach it.
5. Push and PR reject a changed account, ref or generation, an expired or
   reused mandate, protected refs, force-push and `.github/**` changes.
6. Path and symlink escapes are rejected, `run_check` refuses anything not in
   `checks`, the sandbox environment contains no Eggy secrets, and the PAT never
   appears in argv, Git config, logs or the workspace.
7. Sandbox escape tests, run as `bash` commands: reading `/data/eggy.db`, the
   key or another run's directory, writing outside the run, connecting to
   Eggy's port or a non-80/443 port, signalling `eggyd`, and setuid binaries
   all fail. A failed startup probe leaves `bash` unregistered. On Linux CI
   these run for real; on macOS they are skipped and reported as skipped.
8. Crash injection around push and PR proves no duplicate PR, no lost URL,
   and delivery retry without republishing.
9. Fake-model tests cover questions (slot released, clock paused, expiry),
   the budget cap, cancellation, malformed tool calls and a missing toolchain.
10. End to end against a real allowed repository with a real PAT and an
   OpenRouter model: enable from Telegram, run, observe a feature-branch PR,
   private delivery, and no approval or merge.

## Sequence for the implementation plan

1. Config section, `checks`, Extensions card and Agent Runner page skeleton,
   account PAT in settings.
2. Sandbox: `coder` user, `/data` permissions, `sandbox-exec`, Landlock
   ruleset, startup probe, and escape tests; then a re-run of the probe on
   Railway. Bake Go and Node into the image.
3. Workspace lifecycle, confined file tools, `bash`, `run_check`, and a run
   with a fake model.
4. Mandate, publisher and reconciliation.
5. `/code` commands and delivery.
6. Run UI, skill toggles, and a real-model end-to-end run.

## Review focus

The sandbox (A plus B, open HTTPS egress, fail-closed probe), Go and Node in the
image, the
mandate as the sole push/PR authorization in every mode, the
`.github/**` denial, and the limit constants.
