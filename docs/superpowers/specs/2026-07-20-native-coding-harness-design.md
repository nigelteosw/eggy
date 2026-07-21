# Eggy Native Coding Harness Design

## Goal

Eggy currently splits repository work across two engines: the selected reasoning model (DeepSeek Pro by default) answers conversation and read-only questions through narrow, hand-rolled Go tools, while any actual edit is handed off to a detached Codex CLI or Claude Code CLI subprocess with its own internal tool loop, its own context, and its own subscription-billed authentication. This design removes that split. The selected model becomes the only execution engine, for both conversation and repository edits, using real file and shell tools inside `internal/kernel/agent.Loop` — the same tool-calling engine Eggy already runs everything else through. Codex CLI and Claude Code CLI are removed from the codebase.

This supersedes the Codex-delegation sections of `2026-07-19-unified-agent-runtime-design.md` and the CLI-selection behavior added by `2026-07-19-configurable-coding-agent-design.md`.

## Why this shape

`Loop.RunSelected` (`internal/kernel/agent/loop.go`) is already a generic, bounded, multi-step tool-calling loop over any `ports.Model`. Hermes Agent (`github.com/NousResearch/hermes-agent`, already cited as a reference architecture in the prior design) validates the same shape at a larger scale: one `AIAgent` class handles CLI sessions, chat, and cron jobs with no separate coding subsystem, using typed tools (`read_file`, `patch`, `terminal`, `process`) dispatched through the same loop that handles everything else. Eggy adopts Hermes's tool naming directly, since there is no reason to invent different names for the same concepts. Eggy does not adopt Hermes's pluggable sandbox backends (Docker/SSH/Modal/etc.) — Eggy already has a single, simpler sandboxing primitive (`ports.Runner`, workspace-scoped, env-allowlisted, timeout- and output-bounded) that every tool below reuses unchanged.

Pi (`github.com/badlogic/pi-mono`, package `coding-agent`) converges on the same tool shape independently: `read`, `write`, `edit`, `bash` — the same four operations as Hermes's `read_file`/`patch`/`terminal` set, under different names. Two unrelated projects landing on the same minimal tool set is a good sign this is the right surface, not an arbitrary one.

The one deliberate departure from both: Eggy keeps its clone → branch → diff → independent approval (commit → push → pull request) pipeline. Neither Hermes nor Pi has an equivalent. Pi's README says so explicitly — "No permission popups. Run in a container, or build your own confirmation flow with extensions" — and Hermes leans on backend isolation (e.g. its SSH backend, "recommended for security — agent can't modify its own code") instead of an approval gate. Both treat this as the embedder's responsibility, not the harness's; Eggy's approval chain is exactly that responsibility being exercised, not a deviation from either project's philosophy. Eggy needs it because it runs unsupervised against real repositories with real push/PR credentials on a single owner's behalf; that scaffolding is unchanged by this design.

## Tool inventory

### Available in every turn (Assistant lane)

| Tool | Behavior |
|---|---|
| `repository_list` | Unchanged. Lists configured repositories from the runtime registry. |
| `repository_github` | Unchanged. Read-only GitHub issue/PR/check-run metadata via the GitHub adapter — not a file-grep problem, no Hermes equivalent, stays as-is. |
| `read_file` | Renamed from `repository_read`. Bounded line-range read from a file in an ephemeral, read-only checkout. |
| `terminal` | New. Replaces `repository_tree`, `repository_search`, and `repository_status`. Sandboxed shell execution (via the existing `ports.Runner`/`StreamingRunner`) inside the same ephemeral, read-only checkout `read_file` uses — the model runs `grep`, `find`, `ls`, `git status`, `git log`, etc. itself instead of Eggy exposing three bespoke typed tools for the same job. This is a net reduction in bespoke tool surface, matching Hermes's actual minimal footprint. The checkout is destroyed after the turn; nothing here creates a branch, diff, or approval. |
| `repository_modify` | Entry point for a bounded implementation run. It is available on direct owner messages, while scheduled and heartbeat turns use explicit read-only tool allowlists. The hard runtime policy instructs the model to call it only for an explicit owner request to change a repository. |

### Available only inside a `repository_modify` run (a second, bounded `Loop`)

| Tool | Behavior |
|---|---|
| `read_file`, `terminal` | Same tools, now scoped to the branch-checked-out run workspace instead of an ephemeral clone. |
| `patch` | New. Exact old-string → new-string replacement against a file in the run workspace; the match must be unique. Named after Hermes's `patch` tool, but implemented as exact string replacement rather than unified-diff application — simpler and more reliable to implement correctly in Go than a fuzzy-matching diff applier, and this is the same mechanic Claude Code's own edit tool uses. |
| `write_file` | New. Creates a file or replaces its full contents. Handles the "new file" case `patch` cannot (there is no old string to match against). |
| `finish_implementation` | New, required. A structured terminal tool call: `{summary, validation, commit_message, changed_files}`. The inner loop ends when this is called or when its step budget is exhausted. This replaces today's fragile contract — parsing the last stdout line of a CLI subprocess as JSON, with a markdown-fence-stripping fallback (`extractStructuredJSON`) for when the CLI doesn't comply — with an actual typed tool call the loop enforces directly. No Hermes equivalent; this exists because Eggy's approval pipeline needs a structured result Hermes doesn't need to produce. |

`patch`, `write_file`, and `finish_implementation` are never registered in the outer loop's tool registry, so they remain unreachable outside an active implementation run. Scheduled and heartbeat turns also use explicit read-only allowlists that exclude `repository_modify` and `repository_continue`. There is no path by which those non-owner turns can write a file.

## Flow

```text
Direct owner Telegram message
  -> outer Loop.RunSelected                 [unchanged engine, full outer tool set]
       -> model calls repository_modify for an explicit change request
            -> CodingService.Start:
                 clone repository                                    [unchanged]
                 create eggy/<run-id> branch                         [unchanged]
                 run inner Loop.RunSelected, tools = {read_file,      [new: replaces
                   terminal, patch, write_file, finish_implementation} agent.Run() subprocess call]
                 verify branch/HEAD unchanged (already existing check;
                   this is what already catches an attempted git commit) [unchanged]
                 capture diff                                        [unchanged]
                 request commit approval                             [unchanged]
       -> approved commit chains to push approval, then PR approval  [unchanged]
```

The inner loop's step budget is larger than the outer loop's (proposed: 24, versus the outer loop's 8) since real implementation work needs more read/patch/terminal round trips than a chat reply does. It runs against the same selected model as the outer loop — there is no separate "coding model" selection anymore.

## What gets deleted

- `internal/adapters/coding/claudecli`, `internal/adapters/coding/codexcli`
- `ports.CodingAgent` interface, `ports.CodingRequest.ReadOnly` (the flag exists today but nothing ever set it — dead code this design finally either uses or removes; removed, since the read/write split is now expressed as two different tool registries, not a flag threaded through a subprocess call)
- `internal/kernel/services/coding_runtime.go` (`CodingAgentRuntime`, multi-CLI alias selection)
- The `/coding_agent` command, `coding.agents` / `coding.default_agent` config sections
- `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME` environment plumbing, and the README/Dockerfile steps that install and authenticate those CLIs

## Safety model (unchanged)

Every constraint `AGENTS.md` states today continues to hold, enforced the same way it is today:

- Path, environment, timeout, output, and process-group restrictions on `terminal` come from the existing `ports.Runner` — no new sandboxing primitive.
- Commit, push, and pull-request creation remain three independent Telegram approvals. Protected branches remain unpushable regardless of approval.
- The post-run branch/HEAD equality check in `CodingService.Start` is what prevents the model from committing or pushing on its own — this check already exists and already runs regardless of what produced the file changes.
- `patch`, `write_file`, and `finish_implementation` are reachable only inside `repository_modify`; scheduled and heartbeat turns receive explicit read-only tool allowlists that exclude `repository_modify` and `repository_continue`.

## Risk worth naming

DeepSeek doing its own multi-file editing and validation, unsupervised inside the sandbox up to the step limit, is a real increase in what it's trusted to do compared to today's specialized coding-tuned CLI. The approval gate is the backstop — nothing lands without explicit approval — but expect more `patch`/`terminal`/re-`patch` iterations to reach a working diff than Claude Code or Codex needed, since DeepSeek is a general reasoning model, not one tuned specifically for coding.

## Testing strategy

Test-first, per `AGENTS.md`. Focused areas:

- `internal/kernel/agent`: inner-loop construction, step-budget enforcement, `finish_implementation` as the required terminal call, tool-registry isolation (assert `patch`/`write_file`/`finish_implementation` are absent from any `Assistant`-lane tool list).
- `internal/kernel/services`: `read_file`/`terminal` against a fixture checkout (ephemeral, no branch, no diff); `patch` exact-match success and not-unique/not-found failure modes; `write_file` create and overwrite; the full `repository_modify` path against a fake `ports.RepositoryCheckout`/`ports.Runner`, asserting the existing branch/HEAD-unchanged check still fires when a fake tool tries to mutate git state directly.
- Bootstrap: tool registration wiring, capability manifest no longer reports `coding_agent_ready`/`active_coding_agent` (single engine, nothing to report as a separate readiness signal).
- Delete the `claudecli`/`codexcli` adapter test suites along with the adapters.
- Integration: reuse the existing fake-adapter acceptance-transcript pattern from `2026-07-19-unified-agent-runtime-design.md`, adding a transcript that reads a file, patches it, and reaches commit approval without a real model or GitHub call.

Before completion: `make fmt vet test race build`, and `make smoke` when Docker is available.

## Scope limits

No session continuity across turns in this pass — each `repository_modify` call is still one bounded run, same lifecycle as today's coding runs. If a later change wants multi-turn "keep working on the same branch" sessions, Pi's approach is the concrete reference: sessions persisted as JSONL with a tree structure (branching without duplicating history) plus automatic context compaction as the transcript grows. Not designed here; named so the fast-follow isn't starting from a blank page.

No MCP bridge, no change to how memory/calendar/scheduling tools work, no change to `/model` semantics beyond `/coding_agent` no longer existing. This does not add a web framework, ORM, database, agent framework, or native plugin runtime, per `AGENTS.md`.

## References

- Hermes Agent architecture: <https://hermes-agent.nousresearch.com/docs/developer-guide/architecture>
- Hermes Agent tools: <https://hermes-agent.nousresearch.com/docs/user-guide/features/tools>
- Pi coding agent: <https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md>
- Pi harness philosophy: <https://github.com/badlogic/pi-mono/blob/main/README.md>
- Supersedes (repository-delegation sections of): `2026-07-19-unified-agent-runtime-design.md`, `2026-07-19-configurable-coding-agent-design.md`
