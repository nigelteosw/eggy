# Eggy Self-Improvement Implementation Plan

Status: proposed architecture and phased roadmap, 2026-09-06. No runtime changes authorized or implemented. Later coding phases require the explicitly identified design-rule changes; this document does not override AGENTS.md.

**Goal:** Let Eggy use an OpenRouter model to identify and validate improvements, retain useful procedures, and prepare source changes with optional help from the owner's coding subscriptions.

**Architecture:** Keep Eggy's existing agent loop, model adapter, skills store, traces, and approval mechanism. Deliver learning first, then an owner-operated coding handoff. Consider direct coding CLI invocation only after that workflow proves useful and its policy and isolation requirements are agreed.

**Tech stack:** Existing Go services and tests, Markdown skills and briefs, YAML startup configuration, SQLite machine-managed results; official Codex and Claude Code binaries for optional development work.

**Execution:** Implement one phase at a time using the executing-plans skill. For each behavior change, write a failing focused regression, run it, make the smallest implementation, rerun it, then run the required repository checks. This is a roadmap; the conditional execution subsystem needs its own approved detailed design before implementation.

## Recommendation and alternatives

1. **Recommended: learning plus owner-operated coding.** Eggy proposes improvements and produces precise development briefs. You run those briefs in Codex or Claude Code, review the patch, and ship through your existing workflow. Delivers value while preserving current runtime rules.
2. **Conditional next step: approved, bounded coding runs.** Eggy invokes one official coding CLI against an isolated disposable checkout, returning a patch and test evidence. Requires an explicit exception to the declined delegation capability and a real isolation design. No recursive delegation or new Eggy agent loop.
3. **Defer: unattended source mutation and deployment.** This conflicts with unprompted-turn restrictions, the delegation decision, and repository-shipping restrictions. Do not disguise it as a schedule, generic shell tool, or MCP server. It is a separate product decision.

Self-improvement here means improving procedures, prompts, and tested application code. It does not train or update the underlying OpenRouter model's weights.

## Reference projects: borrow selectively

DeepSeek Harness separates model and harness, makes capabilities configurable, and emphasizes inspectable trajectories. Borrow reproducible run context and optional capability composition. Eggy already has ports and bootstrap for composition: do not import Cordis, runtime plugin mounting, interchangeable loops, or unrestricted creator mode. DeepSeek's complete session recording is not a retention policy for Eggy; preserve secret filtering and transient media handling. [DeepSeek overview](https://deepseek.com/harness/en/), [source repository](https://github.com/deepseek-ai/deepseek-harness).

Hermes explicitly learns procedures from experience and loads skills on demand. Borrow the cycle of evidence, procedure, reuse, and correction. Eggy already has the read side. Do not copy its subagent framework, marketplace, channel breadth, or memory-provider machinery. [Hermes repository](https://github.com/NousResearch/hermes-agent), [skills documentation](https://hermes-agent.nousresearch.com/docs/user-guide/features/skills/).

Neither reference establishes that an LLM judging its own patch is sufficient evidence of improvement.

## Current Eggy foundations

- `plugins/models/openaicompat/model.go` and `internal/config` already support OpenRouter configuration. No new model adapter is needed.
- `internal/ports/ports.go` already defines `SkillsStore.Write` and `Delete`; `plugins/skills/store.go` implements them with locking and atomic writes. `internal/kernel/services/skills_tools.go` exposes only `skill_read`.
- The skill index currently reads every Markdown file without the individual read bound and fails the whole list for one malformed file. Fix this before agent authorship.
- Existing traces, recall, tests, and `TODO.md`'s harness regression work are the measurement foundation.
- `internal/kernel/services/repo/primitive_tools.go` deliberately exposes only `read_file`. `plugins/runner/localprocess` has subprocess infrastructure, but its path and environment restrictions are not a sandbox.
- `internal/kernel/turns/turns.go` confines unprompted turns to read-only allowlists. A recurring schedule cannot authorize coding, skill installation, or MCP access.
- Current persistence still includes legacy JSON/file adapters, as TODO.md records. New machine-managed experiment state must use SQLite without expanding that migration debt.

## Subscription feasibility and execution location

OpenRouter API billing is separate from Codex and Claude subscriptions. Keep OpenRouter as the conversational model provider; a coding CLI is a task executor, not another implementation of `ports.Model`.

Codex supports `codex exec`, JSON event output, and saved CLI authentication. Official documentation also describes account-auth automation, with restrictions and credential-handling requirements. Start with the owner's locally authenticated CLI; do not copy account credentials into Eggy's config or traces, or seed them into public-repository CI. Validate the owner's actual account and installed version before promising unattended execution. [Official non-interactive documentation](https://learn.chatgpt.com/docs/non-interactive-mode).

Claude Code supports subscription sign-in to its unmodified binary. Its guidance distinguishes this from third-party applications routing requests through subscription credentials. Scripted `--bare` mode uses API authentication and does not use subscription login. Therefore use the subscription initially through an owner-operated Claude Code session; treat unattended subscription-backed invocation as a feasibility gate, not a guaranteed feature. Never extract OAuth tokens or build a Claude subscription proxy. [Authentication](https://code.claude.com/docs/en/authentication), [programmatic execution](https://code.claude.com/docs/en/headless), [credential rules](https://code.claude.com/docs/en/legal-and-compliance).

Both binaries are installed on the inspected development machine, but login state, subscription tier, quota, and successful model execution were not checked.

If Eggy runs on Railway, it cannot invoke binaries on your Mac through a local subprocess. Phase 3 deliberately uses a manual brief transfer. Phase 4 assumes execution on an explicitly configured development host with the official CLI available. A remote Mac worker or companion daemon would violate another declined capability and is outside this plan. Do not install coding credentials and a writable production checkout into the running production container as a shortcut.

## Phase 1 — Establish measurable improvement

Files: extend existing tests under `internal/kernel/agent`, `internal/kernel/services`, `internal/kernel/turns`, and `plugins/models/openaicompat`; use `plugins/memory/sqlite/traces.go` for existing trace access. Update the existing TODO items when work lands, rather than adding a duplicate roadmap there.

- [ ] Assemble 10–20 sanitized representative tasks: multi-step repository lookup, skill selection, recall, compaction steering, approval rejection/expiry, provider failure, and forbidden unprompted actions.
- [ ] Record baseline outcome, tool/model call counts, prompt and cached tokens, latency, and available provider usage/cost. Keep live paid samples owner-triggered.
- [ ] Keep a held-out subset unavailable to candidate generation. Compare the same model, task inputs, and tool conditions; repeat stochastic live cases and report sample size.
- [ ] Require all safety regressions to pass. Accept a candidate only when it improves task success or lowers measured cost/latency without reducing correctness. Set task-specific thresholds before evaluating the candidate.

Deletion budget: zero production lines initially, zero config keys/tools/new record types/background loops; extend existing tests and use existing traces. Baseline report is owner-facing Markdown. No evaluation framework dependency.

## Phase 2 — Let Eggy improve its procedures

Files: `plugins/skills/store.go` and tests; `internal/kernel/services/skills.go`, `skills_tools.go`, and tests; `internal/bootstrap/app.go`; approval and unprompted-turn tests.

- [ ] Bound per-file reads before allocation and bound the aggregate summary index. A malformed file must be reported without making all valid skills unusable. Report capacity exhaustion explicitly.
- [ ] Extend the existing skill surface to support read, write, and delete with action-specific effects. Preserve read-only access in normal mode and unprompted turns; write/delete retain their own payload-bound calls through the existing approval mechanism. Retire the old read-only definition if replacing its schema; do not maintain two equivalent APIs.
- [ ] Pass the existing `SecretGuard`, populated from `Secrets.Values()`, through the write path. Never apply the memory-only `InternalTool` exemption to skills.
- [ ] Present the exact proposed content and target to the owner before installation. Protect replacement from stale approvals by binding the expected prior-content digest and checking it under the file lock; add a narrow conditional-write port only if required, rather than doing an unlocked read/check/write.
- [ ] After a substantial owner-requested task, propose a skill only when there is a reusable procedure backed by observed success. Include when to use it, concrete steps, verification, and failure conditions. Update an existing matching skill before adding another.
- [ ] Test traversal/symlinks, oversize files, malformed metadata, aggregate bounds, secrets, concurrent replacement, approval expiry/rejection, strict/normal/auto behavior, and denial on every unprompted path.

Done when an approved procedure can be reused successfully on a second task, while rejected or stale writes never reach disk. Preserve the prior version in the reviewed change record using existing durable facilities; do not introduce a second skill store.

Deletion budget: replace the unbounded index and existing tool definition; preliminary allowance ~140–250 net production lines including conflict checking, zero new config keys/loops, no net extra tool definition if consolidated, existing Markdown skills and approval records. Refine the estimate in the phase design.

## Phase 3 — Produce coding briefs for your subscriptions

Files: an owner-installed Markdown skill in the configured skills directory, using the existing flat `name.md` format. No runtime coding tool.

- [ ] Define a brief with the observed failure, sanitized evidence, repository/base commit, permitted scope, acceptance tests, deletion budget, and forbidden changes.
- [ ] Eggy generates the brief in an owner-requested conversation. You open it in your authenticated Codex or Claude Code session against a separate checkout.
- [ ] Require the coding session to reproduce the failure first, make the scoped change, and return the diff, test commands/results, and limitations. Keep credentials and production data out of the checkout.
- [ ] You review and ship through your existing development workflow. Eggy can review evidence and propose a procedural lesson afterward; it gains no commit/push/PR/merge authority.
- [ ] Try three real improvements before designing automation: one regression fix, one measured prompt-cost change, and one reusable skill. Record time saved and review effort.

Suggested routing: OpenRouter diagnoses and writes the brief; choose one coding subscription for implementation; use the other for review only when complexity justifies the extra usage. Unused quota alone is not a reason to manufacture changes.

Deletion budget: zero production lines/config keys/tools/machine-record types/loops, one owner-facing Markdown skill. This is the smallest useful version of subscription-assisted self-improvement.

## Phase 4 — Conditional approved coding execution

Entry gate: Phase 3 demonstrates demand; revise AGENTS.md explicitly to permit bounded external coding execution. This remains delegation even without child conversations, and cannot be introduced by renaming it. Keep unprompted mutation forbidden and shipping outside Eggy.

Proposed files: `plugins/tools/coding/` for the optional tool and task contract; provider-specific CLI implementations under `plugins/coding/codex/` and `plugins/coding/claudecode/`; `internal/config/config.go`; inline bootstrap wiring; a narrow provider-neutral port only if the existing runner cannot express the task contract. Avoid a general provider lifecycle framework.

- [ ] First implement and test one explicit owner command plus a skill. Expose one optional gated tool only if conversational invocation is necessary; do not add an MCP wrapper as a way around approvals.
- [ ] Bind each approved run to repository identity, base commit, task, allowed paths, chosen executor, and resource limits. One run approval authorizes only the disposable patch attempt, never applying or shipping its result.
- [ ] Use one blocking, deadline-bound execution in the existing turn, concurrency one. No queue, polling daemon, child Eggy loop, or recursive delegation. Begin with a 15-minute deadline, one attempt, and explicit retry by the owner. Check interaction with restart draining.
- [ ] Enforce isolation outside the prompt: disposable source copy, no production filesystem, no Docker socket, no shipping credentials, no inherited personal MCP servers/hooks, and controlled network/credential access. A Git worktree alone is insufficient because it shares Git metadata. CLI login secrets must not be available to generated shell commands or tests; if the chosen platform cannot enforce that separation, stop at Phase 3.
- [ ] Execute fixed binary paths with argv and stdin, never a shell-interpolated model command. Parse bounded structured output; distinguish rate limit, auth failure, timeout, malformed output, cancellation, and test failure. Kill process groups and bound both stdout and stderr.
- [ ] Recompute the actual diff and base identity independently of the model's summary. Reject out-of-scope files, symlink escapes, and changes to approval policy, secrets, deployment configuration, or protected evaluation fixtures.
- [ ] Verify the patch in a fresh credential-free environment using trusted acceptance tests. Record patch digest, base commit, executor/version, results, and failure status in SQLite if durable run tracking is needed. Do not persist raw CLI sessions, media, or account credentials.
- [ ] No silent fallback from subscription to paid API. Rate limits return a blocked result; another executor requires an explicit owner-selected run. Do not infer remaining allowance from time of day.
- [ ] Fake-subprocess tests cover argument construction, isolation controls, partial output, timeout, cancellation, restart, stale base, repeated request, auth errors, disabled registration, and quota exhaustion. An owner-authorized live run validates each supported CLI version and billing path.

Done when one approved request yields a reproducible patch and independent validation, with no changes to the source deployment. If work exceeds the synchronous deadline, report it as incomplete; an asynchronous job subsystem needs a separate proposal.

Deletion budget: remove the manual invocation step, but this is a net-new capability, not a shrinking refactor. Preliminary planning envelope ~500–900 production lines plus isolation integration, ~4–6 configuration fields, at most one optional tool, at most one SQLite run-record type, zero background loops. If robust isolation needs a separate service, return to the architecture decision rather than forcing this estimate.

## Applying and releasing changes

Keep application, commit, push, PR creation, merge, and deployment owner-operated. A coding-run approval cannot approve an unseen patch. If protected shipping ever becomes a separate requested feature, each operation requires its own action, executor, exact payload-bound approval, and protected-branch denial under AGENTS.md.

For source changes, use the normal build/deploy path and retain a known-good artifact. Restart reloads configuration; it does not deploy changed Go source. Test rollback before any future automated release work. Protect state migrations independently: an old binary may not understand a newer schema.

## Verification and first milestone

For each implemented behavior change: focused failing test, focused passing test, then `make fmt vet test race build`; `make smoke` when Docker is available, otherwise report the blocker. Run comparison workloads after correctness checks. No tests or live model calls were run for this planning-only change.

First milestone: OpenRouter-backed Eggy identifies a real failed workflow, proposes one reviewed skill, reuses it successfully, and produces one precise coding brief that the owner completes with an existing subscription. Advance to direct execution only when that workflow demonstrates enough value to justify the added authority and runtime footprint.
