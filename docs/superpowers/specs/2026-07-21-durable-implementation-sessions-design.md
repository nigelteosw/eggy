# Durable Implementation Sessions Design

## Goal

Turn Eggy's current bounded, one-shot native implementation loop into an explicit,
owner-triggered implementation session that can be resumed after a Telegram request
or a Railway restart. The session must preserve the useful working record of an
implementation—its tool transcript, concise activity events, checkpointed context,
branch, workspace, and approval status—without changing Eggy's Go-native runtime or
its independent commit, push, and pull-request approvals.

This is a fast-follow to `2026-07-20-native-coding-harness-design.md`. It replaces
that document's deliberate "no session continuity across turns" scope limit.

## Product decisions

- Sessions start only from an explicit owner implementation request. Eggy does not
  audit repositories, identify improvements, or open self-improvement work on its
  own.
- Resumption is explicit. The owner asks Eggy to continue a named or most-recent
  session; a restart never resumes a model run automatically.
- Sessions survive process restarts and Railway deploys, including uncommitted
  workspace changes.
- Telegram shows concise semantic milestones, not raw `used terminal` messages.
- The detailed inner-tool transcript is retained so Eggy can explain what changed
  and why, but hidden model reasoning, secrets, and unbounded command output are
  never persisted.
- Eggy borrows session ideas from Pi (lifecycle, event streaming, bounded context
  and compaction) and Hermes (durable session records, concise resumption recaps,
  and human-friendly session identity). It does not import either project's runtime
  or storage model.

## Architecture

### Boundaries

The implementation remains ports-and-adapters:

| Layer | Responsibility |
|---|---|
| `internal/kernel` | Provider-neutral session state machine, transcript policy, event classification, compaction and resume orchestration. |
| `internal/ports` | Session-store and session-event interfaces plus provider-neutral session records. |
| `internal/adapters` | File-backed durable-session store and Telegram event projection. JSON and filesystem concerns stay here. |
| `internal/bootstrap` | Constructs the store, injects it into the implementation service and connects events to Telegram progress delivery. |

The selected `ports.Model` and native `agent.Loop` remain the implementation engine.
Provider adapters are unchanged. `patch`, `write_file`, and `finish_implementation`
remain inner-loop-only tools; session support does not create a new write path.

### Identity and relation to coding runs

Each implementation session has a generated `SessionID`. A new coding run uses that
same value as its `CodingRun.ID`, avoiding an extra mapping at approval time. The
existing `CodingRun` in `/data/state.json` remains the source of truth for the
commit, push, and pull-request workflow. Old state files and pre-session runs remain
valid and can still be shipped.

The durable session record is the source of truth for agent history, checkpoints,
events, and resumability. It is deliberately separate from the global state file so
the existing state schema needs no version migration.

### Persistent layout

The JSON-file adapter persists one session directory beneath the existing durable
volume:

```text
/data/sessions/<session-id>/
  session.json       # task, repository, branch, workspace, status, timestamps
  events.jsonl       # append-only transcript and concise semantic events
  context.json       # current compaction checkpoint and retained recent context
```

Every record includes a sequence number and timestamp. `session.json` and
`context.json` are written atomically (temporary sibling then rename); events are
appended before the equivalent Telegram progress event is emitted.

The runner workspace must also be durable. Railway deployments must configure
`runner.root` under the mounted volume (for example `/data/runs`), not the sample
configuration's `/tmp/runs`. A resumable-session deployment with an ephemeral
workspace root must fail clearly at startup rather than claim it can preserve an
uncommitted diff. Local deployments may use any intentionally durable configured
directory.

## Session record and state machine

A session captures:

- owner instruction, repository name, branch, base revision and workspace path;
- selected model identifier and implementation-prompt version;
- current status, timestamps and active-run lock;
- current diff/validation facts and the associated `CodingRun`/approval state;
- a rolling compaction summary and recent context window; and
- append-only transcript and semantic event sequence.

The allowed state flow is:

```text
created -> running -> awaiting_commit_approval -> committed
        -> awaiting_push_approval -> pushed
        -> awaiting_pr_approval -> completed

running -> interrupted | blocked | awaiting_instruction | cancelled
interrupted | blocked | awaiting_instruction -> running       (explicit continue only)
awaiting_commit_approval -> running                           (explicit continue; invalidates approval if diff changes)
```

Only one active model run may hold a session lock. A concurrent continue request
returns the current status instead of opening a competing workspace or model loop.
Completed and cancelled sessions are not resumed; a new owner request creates a new
session.

## Transcript, events and model context

### Transcript policy

`events.jsonl` stores the owner instruction, assistant-visible tool-call arguments,
sanitized tool results, structured `finish_implementation` results, validation facts
and lifecycle changes. It never stores hidden reasoning.

Terminal records retain the command, exit code, truncation marker and a bounded,
redacted output excerpt. File and patch records retain affected relative paths and
diff facts. The existing environment allowlist, output limits, timeouts, path checks
and process-group handling remain authoritative; session storage does not bypass or
relax them.

### Semantic event projection

The inner loop emits structured tool-start and tool-end events rather than only a
tool name. A deterministic kernel classifier converts those into concise events such
as:

```text
Plan: add durable implementation sessions
Inspected: internal/kernel/agent/loop.go
Edited: internal/kernel/services/session.go
Validation: go test ./internal/kernel/services passed
Blocked: test fixture requires a durable workspace root
Ready for commit approval
```

Telegram renders those events as a single live status/timeline with meaningful
milestones. It does not render raw output or an event for every implementation
detail. The complete sanitized record remains available to answer owner questions
such as "what changed?" and "why did it stop?".

### Compaction

Before a model call would exceed the configured session context budget, the kernel
creates a checkpoint containing the objective, decisions, inspected and changed
files, validations, known diff state, blockers and recommended next action. The next
implementation call receives the checkpoint plus the recent relevant transcript,
not the entire historical log.

Raw events stay on disk for audit and explanation. Compaction is visible in Telegram
as one concise milestone. The input budget and retained-tail size are explicit
configuration owned by the implementation runtime, so different selected models can
use different safe context limits without leaking provider types into the kernel.

## Start and resume flows

### Start

1. An explicit owner implementation request reaches `repository_modify` from a
   direct owner message. Scheduled and heartbeat turns use read-only tool allowlists
   and cannot start an implementation session.
2. Eggy creates the durable session and workspace, clones the trusted configured
   repository and creates the isolated branch.
3. The native implementation loop runs with the existing inner-only tool registry.
   It appends transcript and semantic events as it works.
4. Eggy captures the diff and validation facts, persists them, and requests the
   independent commit approval.

### Resume

1. The owner explicitly asks to continue a session. Eggy selects the named session
   or the owner's most recent resumable session and displays a compact recap:
   objective, branch, completed changes, latest validation and current next step.
2. Eggy acquires the session lock, verifies the persisted workspace and branch,
   rebuilds model context from the checkpoint plus retained event tail, then runs the
   same native inner loop.
3. If the session was awaiting commit approval and the new work changes the diff,
   the old approval is invalidated. A new exact commit approval is required after
   Eggy reaches a stable result.

No resume operation automatically commits, pushes, creates a pull request, or
continues after a process restart. All three existing approval gates remain separate.

## Failure and restart behavior

- A model error, tool error, timeout, exhausted tool-step budget or Telegram delivery
  failure records an explainable `blocked` or `interrupted` event. The workspace,
  branch, transcript and checkpoint stay intact.
- On bootstrap, a session persisted as `running` is marked `interrupted`; its process
  is not recreated. The owner must explicitly continue it.
- If the workspace or branch is missing, Eggy marks the session blocked with an exact
  recovery reason. It never silently create a new branch and pretend it is the same
  session.
- Approval payload checks continue to compare the actual branch, revision and diff at
  each shipping action. A changed workspace invalidates the corresponding approval.
- Retention and cleanup remain explicit owner operations in this first version. No
  active session is pruned automatically.

## Safety invariants

This design does not change the following constraints:

- configured repositories are trusted, but tool paths, environment, timeout, output
  and process-group restrictions remain enforced by `ports.Runner`;
- implementation work remains isolated in a branch and restricted workspace;
- `main` and other protected branches remain unpushable;
- commit, push and pull-request operations each require their own exact approval;
- the outer conversational tool registry still cannot invoke write tools directly;
- only explicit owner requests can start or resume implementation work.

## Testing and verification

Implement test-first. Focused tests cover:

- session creation, append ordering, atomic checkpoint writes and schema-compatible
  loading of old `/data/state.json` files;
- transcript sanitization, command-output bounding and deterministic semantic event
  classification;
- compaction retaining the required checkpoint and recent tail;
- explicit resume restoring the same branch/workspace/context, while rejecting a
  concurrent run;
- bootstrap recovery marking an in-flight run interrupted without auto-resuming it;
- durable-workspace configuration validation;
- Telegram milestone rendering and concise resumption recap;
- approval invalidation after resumed work changes a pending diff; and
- existing protected-branch and independent commit/push/PR approval behavior.

Before completion run `make fmt vet test race build`; run `make smoke` when Docker is
available.

## Non-goals

- No autonomous repository audits, self-improvement tasks or automatic PRs.
- No external Pi or Hermes runtime, database, web framework, ORM, agent framework or
  plugin runtime.
- No hidden-reasoning persistence, raw credential storage or unlimited terminal-log
  retention.
- No cross-platform handoff, session search UI, forking UI, or automated session
  cleanup in this implementation. They can be considered later once the core owner
  triggered resume path is reliable.

## References

- Pi coding-agent SDK session lifecycle, compaction and event model: <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md>
- Hermes Agent session persistence, compact resume recap and context guidance: <https://hermes-agent.nousresearch.com/docs/user-guide/sessions/>
