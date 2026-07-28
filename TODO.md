# Eggy roadmap — complexity reduction

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; completed implementation history remains in git.

This revision replaces a feature roadmap with a reduction plan. Nothing below
adds a capability. Remove an item when its change and focused tests have
landed.

---

## Diagnosis

Measured on the current tree, not estimated.

**Size.** 21,399 lines of production Go, 17,660 of test. 20 tools on an
ordinary owner turn (25 with Calendar, more with MCP). 17 top-level slash
commands across ~40 catalog paths. 11 config sections. One package,
`internal/kernel/services`, holds 30 source files.

**Separation of concerns is not the problem.** `ports` / `kernel` / `plugins`
is a real boundary and it mostly holds. The loop (`agent/loop.go`, 287 lines)
is small and correct. What is out of hand is the *number* of concerns, and a
handful of specific leaks. Refactoring will not fix the first; only deleting
capabilities will.

**Per-turn context floor: 13,507 bytes (~3.4K tokens)** with empty
SOUL/USER/MEMORY, no skills installed, Calendar off, MCP off:

| section | bytes |
| --- | ---: |
| hard runtime policy | 4,451 |
| tool schemas (20 tools) | 7,855 |
| capability manifest | 542 |
| SOUL.md / USER.md / MEMORY.md | 495 |
| temporal context | 132 |
| skills index | 32 |

With Calendar wired, real durable docs, and one MCP server this is comfortably
25–30KB before the owner types anything.

**This is not the problem.** Measured against comparable agents, Eggy's context
assembly is already good — see the comparison below. The defects are that the
policy blob is unconditional and that the section order defeats prompt caching.
Both are correctness issues, not budget issues. Do not optimize bytes.

---

## Where Eggy actually sits

Checked against the three agents this project was modelled on (July 2026).

| agent | system prompt floor | tools | skill loading |
| --- | --- | ---: | --- |
| **Pi** (`badlogic/pi-mono`) | <1,000 tokens | 4 | `AGENTS.md` on demand |
| **Eggy** | ~3.4K tokens | 20 | index + `skill_read` ✅ |
| **Hermes** (Nous) | not published | dynamic | index + `skill_view` ✅ |
| **OpenClaw** | 40–90KB (layers 1–6: 20–40KB framework; layers 7–8: 20–50KB workspace files) | dozens | **none — full injection** |

Three things follow, and they invert the premise that Eggy has a disclosure
problem:

**Eggy's skill disclosure is ahead of OpenClaw's, not behind it.** OpenClaw
injects every installed skill's full `SKILL.md` into the system prompt at init.
At 83 skills that truncates — only 41 are visible — and burns context on skills
never triggered. The fix (advertise name+description ~100 tokens each, add
`load_skill(name)`) is open issue #39945, closed with no maintainer activity.
That is precisely what `SkillSummary` + `skill_read` already does here. Eggy
structurally cannot hit OpenClaw's truncation failure.

**Pi is the argument for the P0 deletion below.** Four tools — `read`, `write`,
`edit`, `bash` — and a sub-1,000-token system prompt. Mario Zechner's stated
reasoning: frontier models already understand coding agents from RL training,
so rather than adding specialized tools, trust the model to invoke CLI
utilities through bash. Eggy already ships that exact primitive (`terminal`),
and then adds 16 more tools around it. `web_search` is the clearest case — Pi
would have the model curl it.

**Hermes validates the P0 conditioning fix and adds one Eggy is missing.** It
assembles the prompt in three ordered tiers — stable (identity, tool guidance,
skills index), context (project files), volatile (memory, profile, timestamp) —
explicitly ordered so the stable prefix survives provider-side prompt caching
and volatile data at the tail cannot invalidate it. Eggy's ordering does the
opposite in one place; see P0 below.

---

## P0: Instructions do not track the turn — LANDED

`hardRuntimePolicy` was a single 4,451-byte constant sent verbatim on every
turn, so a heartbeat (12 tools, no write primitives) still received the
paragraphs governing `propose_change`, the commit → push → pull-request
approval chain, and scheduled-turn draft-PR rules.

Now split into `coreRuntimePolicy` (binds every turn, names no tool) plus
`runtimePolicyFragments` keyed by the tools they govern.
`renderRuntimePolicy` reads `CapabilityManifest.Tools` — the same field the
manifest section renders — so the policy and the manifest cannot disagree
about what a turn can do. Turn-kind rules moved to the turn that owns them:
`agent.ScheduledTurnMessage` via the new `Policy.Extra`, and the heartbeat's
paragraph was deleted outright as `Service.Heartbeat` already appended it
verbatim.

Assembled policy size: owner 3,360 (−1,091), heartbeat 2,433 (−2,018),
read-only 2,135 (−2,316). The saving is incidental; the invariant is the
point, and `TestHeartbeatPolicyNamesNoToolOutsideItsAllowlist` asserts it
structurally rather than by byte count.

- [ ] Remaining: prose still duplicated between policy fragments and tool
      descriptions. `memory`'s description is 551 bytes and the memory fragment
      re-explains the byte budget on top of it; `propose_change` has a 414-byte
      description plus a fragment paragraph. Pick one home per fact.

## P0: Section order defeats prompt caching — LANDED

The capability manifest sat second — it changes on `/model` and on MCP
connect — while SOUL.md, which changes almost never, sat fourth. Providers
cache the longest stable *prefix*, so one `/model` switch re-encoded SOUL.md,
USER.md, and MEMORY.md behind it on every subsequent turn.

Reordered most-stable-first: policy → SOUL.md → skills index → capability
manifest → USER.md → MEMORY.md → temporal context. Trust order is unaffected;
the rationale is in `Instructions`' doc comment.

The caveat is discharged: `openaicompat.Generate` maps each `ports.Message`
1:1 onto a `providerMessage` preserving order and role, with no concatenation
or reordering, so the stable prefix reaches the wire intact.

- [ ] Confirm the win empirically rather than by construction.
      `ModelUsage.CachedPromptTokens` already decodes
      `prompt_tokens_details.cached_tokens`, so the cache-hit rate is already
      being recorded — surface it in `/usage` and compare before/after.

## P0: Decide what Eggy is, then delete the rest

The reduction that matters is not structural. Approximate production LOC per
capability cluster, including its plugin, kernel service, tools, and commands:

| cluster | LOC | notes |
| --- | ---: | --- |
| slash commands | 2,393 | two grammars (Telegram + CLI) over one catalog |
| MCP | 1,816 | two transports, OAuth, per-tool failure policy |
| web UI + webchat | 1,345 | second surface; a pull surface only |
| memory + embeddings | 920 | SQLite + vector recall |
| shipping / changes / checks | 935 | commit → push → PR → checks watcher |
| calendar | 777 | OAuth, ETag binding, approval-gated mutations |
| web search | 762 | three interchangeable providers |
| scheduler + heartbeat | 701 | cron, quiet hours, weekly proactive limits |
| skills | 688 | |

- [ ] Choose the core. The defensible one is: **one chat surface, one
      repository workflow, durable context, skills.** That is Claude Code's
      shape and it is what the README's own framing claims Eggy is.
- [ ] Apply Pi's test to every tool before it survives: *could the model do
      this with `terminal` and a CLI?* Pi ships four tools on the theory that
      frontier models already know how to drive a shell, and Eggy already has
      the shell. `web_search` fails this test outright. `repository_github`
      largely fails it — `gh` is a CLI. `calendar_*` passes only because there
      is no authenticated CLI in the workspace, which is an argument for MCP,
      not for five kernel tools.
- [ ] Cut candidates, in descending order of cost-to-value:
      - **Web search, three providers → one, or none.** 762 lines to give the
        model a search tool an MCP server could provide. Strongest cut.
        `plugins/search/{tavily,searxng,googlecse}` are interchangeable
        adapters serving one interface; keep at most one.
      - **Calendar.** 777 lines, an OAuth flow, encrypted token storage, an
        approval action family, ETag binding, and five tools (~2.5KB of turn
        context) for a capability an MCP server already covers. This is the
        clearest case of something that should have been MCP from the start.
      - **Embeddings / vector recall.** `SearchSimilar`, the embedding worker,
        and the `embeddings` config block exist so `recall_conversation` can do
        semantic search. Text search over a single owner's history is very
        likely enough. Cutting this removes a provider dependency, a background
        worker, and a config section.
      - **The CLI grammar, or the Telegram grammar.** `internal/commands` runs
        one catalog through two parsers (`ParseTelegramInput`, `ParseCLIArgs`).
        If `eggy <cmd>` is only ever used for setup, shrink it to the three or
        four commands setup actually needs and drop the shared-catalog
        machinery.
- [ ] Whatever survives: each cut removes its config section, its tools, its
      slash commands, its approval actions, and its README section in the same
      change. A half-removed capability is worse than the whole one.

Do not treat this list as decided. It is the decision that has to be made
before any of the structural work below is worth doing.

## P1: MCP must be a plugin, not an agent modification

`mcp` appears in 14 non-test files under `internal/`. Most are legitimate
(bootstrap wiring, config schema). These are not:

- [ ] `internal/commands` imports `plugins/tools/mcp` directly.
      `MCPCommands` returns `mcpadapter.ServerStatus` and
      `mcpadapter.ProbeResult` (`commands.go:184`). A plugin type crosses into
      `internal/`, breaking the repo's own standing constraint. Declare
      neutral status/probe structs in `internal/commands` or `internal/ports`
      and have the adapter map to them.
- [ ] `commands.SetMCP` is a post-construction setter whose own doc comment
      documents a nil-interface trap it cannot prevent (`commands.go:229-235`),
      and `bootstrap/mcp.go:69-77` re-documents the same trap at the call site.
      Pass it through `commands.Options` at construction like every other
      collaborator and delete both comments.
- [ ] `agent.Loop.SetDynamicTools` exists solely for MCP. The loop grew a
      second, mutable tool source and a per-turn merge (`loop.go:108-131`) to
      accommodate one plugin. Either make *all* tools come from one
      `func() []ports.Tool` provider the registry also satisfies — so the loop
      has one tool source, not two — or accept a restart-to-reload MCP and
      delete the dynamic path entirely. The second is cheaper and matches how
      `config.yaml`-owned servers already behave.
- [ ] `internal/web` carries three MCP-specific config routes backed by
      `config.SetMCPServer` / `RemoveMCPServer` / `GetMCPServersConfig`
      (`web.go:94-96`, `config_mutate.go:143-218`). Either add a generic
      config-section route and drop the MCP special case, or drop the routes
      and let `config.yaml` be the only way to define a server (which the
      existing "Runtime `/mcp add` is out of scope" decision already implies).
- [ ] `NewHTTPHandlerAt` takes the MCP OAuth callback as a trailing variadic
      `...http.Handler` (`web/server.go:11`). Make it a named field on an
      options struct; a variadic handler slot is a hole, not a parameter.
- [ ] Remove MCP from `hardRuntimePolicy` (`prompt.go:55`). Once instructions
      track the allowlist (P0), "reaches no MCP tool" is expressed by the tool
      list not containing one, and does not need a sentence.

Net effect: MCP becomes a plugin that supplies tools and a few status
commands. Nothing in `internal/kernel` mentions it.

### Deferred MCP tool schemas — not yet

An earlier draft of this file proposed building a tool-search mechanism (names
resident, schemas fetched on demand) for MCP. **Do not build it yet.**
Anthropic's guidance for the Tool Search Tool is to defer when tool definitions
exceed ~10K tokens, and to skip deferral entirely below ~10 tools, where search
latency costs more than the tokens saved. Eggy's 20 tools cost ~2K tokens —
a fifth of the threshold. Claude Code's own `ENABLE_TOOL_SEARCH=auto` encodes
the same rule: load upfront while schemas fit in 10% of the window, defer only
the overflow.

The failure mode is real but not yet present: 50+ MCP tools across a few
servers runs 55–72K tokens upfront, and deferral cuts that ~85%. Claude Code
measured a 46.9% total-token reduction in real use, plus tool-selection
accuracy gains (Opus 4.5: 79.5% → 88.1%).

- [ ] Add a `/context` line reporting MCP tool schema bytes separately from
      kernel tool schema bytes, so the threshold is observable.
- [ ] Revisit deferral only when MCP schemas alone exceed ~10K tokens. Until
      then, resident schemas are the correct choice and the simpler one.

## P1: Collapse `internal/kernel/services`

30 files in one package with no internal structure. The repository cluster
alone is `changes.go`, `change_tools.go`, `shipping.go`, `checks.go`,
`workspace_sessions.go`, `repositories.go`, `repository_tools.go`,
`repository_metadata_tools.go`, `workspace_path.go` — nine files and five
overlapping nouns (Change, ShipTarget, WorkspaceBinding, Repository, Session).

- [ ] Split into subpackages along the seams that already exist:
      `services/repo`, `services/context`, `services/schedule`,
      `services/diag`. The split is a rename plus import fixes; the types are
      already separated, only the directory isn't.
- [ ] While splitting, look hard at whether `Changes`, `Transcripts`, and
      `WorkspaceSessions` need to be three stores. Each has its own file
      format, its own lifecycle, and its own doc comment explaining why it is
      not the other two. That explanation-per-boundary is itself the signal.

## P1: Correctness and dead weight

- [ ] `ports.State.RecentMessages` (`ports.go:325`) is dead. Nothing reads or
      writes it; the SQLite memory store replaced it. Delete the field and
      note the schema change.
- [ ] `ports.State.Calendar` is retained only for a boot migration
      (`ports.go:333`). Fold into the migration exit plan below.
- [ ] Path containment is lexical, not symlink-aware.
      `resolveWorkspacePath` (`workspace_path.go:20`) and `Runner.withinRoot`
      (`runner.go:178`) use `filepath.Abs` + `filepath.Rel` with no
      `EvalSymlinks`, so a symlink inside the workspace pointing at `/` passes
      both. The `terminal` tool runs `sh -c` with an arbitrary command anyway,
      so containment was never a real boundary. Fix the comment that claims
      "a primitive can never touch a file outside the checkout" and state the
      trusted-repository threat model plainly, rather than adding
      `EvalSymlinks` and implying a boundary that `sh -c` defeats.
- [ ] Give migration code an exit plan. `legacy_coding_runs.go`,
      `migrate_auth.go`, `migrate_cron.go`, and `mcp/oauth_migrate.go` have
      none. Single-user, single-replica, deploy under our control: pick a date,
      write it in each file, delete on that date.
- [ ] State plainly in `README.md` that the web password is a plaintext shared
      secret in config, not a hash. Defensible for a single-owner app; should
      be a documented choice.

## P1: Close the MCP authorization gap

Unchanged and still open. An MCP tool call is an arbitrary remote side effect
gated by nothing but a failure cooldown. Owner-prompted turns invoke them
ungated; the only mitigation is that unprompted turns cannot reach MCP tools.

- [ ] Decide whether MCP tool calls need an approval classification, or whether
      the trusted-server assumption is stated and accepted. Either is a
      defensible answer; the current state — an unstated assumption — is not.

## P2: Documentation weight

`README.md` (309 lines / 29KB), `docs/ARCHITECTURE.md` (511 lines / 29KB), and
this file are three overlapping descriptions of the same system. Code comments
carry a fourth: `loop.go` is ~40% prose, much of it litigating design
alternatives rather than explaining behavior.

- [ ] After the deletions above land, rewrite README as a short operator's
      guide and let `docs/ARCHITECTURE.md` be the only narrative.
- [ ] When a comment explains *why an alternative was rejected*, that belongs
      in the commit message or ARCHITECTURE.md, not above the function. Trim on
      touch; do not do a documentation-only sweep.

## P2: Deferred

Everything here is real but should not be started until the P0 scope decision
is made — several of these items are on code that may be deleted.

- [ ] Continue an existing pull request on its own branch rather than branching
      from trunk and opening a duplicate.
- [ ] Resolve the pull request a continuation belongs to from the thread alone.
- [ ] Save a bounded patch and validation artifact before workspace cleanup.
- [ ] Cleanup and retention diagnostics for abandoned workspaces.
- [ ] An explicit discard operation that cannot affect the owner's checkout.
- [ ] `/skills browse <repo-url>` and `/skills clone <repo-url> <path>`,
      read-only, no bulk importer.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.
- [ ] Evaluate container-per-run isolation. Three subprocess surfaces
      (`terminal`, repository runs, stdio MCP children) execute as Eggy's own
      user with Eggy's own filesystem access. Each constructs a minimal
      environment; none is isolated.

## Operational follow-ups

Manual deployment steps, not code changes.

- [ ] Set `server.trusted_proxy_hops: 1` in Railway's `/data/config.yaml`. It
      defaults to 0, which behind Railway's proxy keys the web login throttle
      on the proxy's address for every request.
- [ ] Reset Railway's `/data/config.yaml` so the next boot generates the
      current config shape.

---

## Standing constraints

Properties every change must preserve. Deliberately shorter than the previous
version: a constraint list long enough to need re-reading before each change is
part of the complexity problem.

**Architecture**
- `internal/kernel` and `internal/ports` stay provider-neutral. Adapters live
  under `plugins/` and are wired only in `internal/bootstrap`. `internal/`
  packages do not import plugin types. (Currently violated by
  `internal/commands` — see P1.)
- The primitive tool surface (`read_file`, `terminal`, `patch`, `write_file`)
  is kernel-owned and defined exactly once. Nothing shadows a primitive.
- Operational state is file-backed, so production runs exactly one `eggyd`.

**Safety**
- Nothing lands in a repository without payload-bound authorization and a
  human-reviewed pull request. Protected branches stay unpushable.
  Eggy never merges.
- Unprompted turns (scheduled, heartbeat) may only *propose*: isolated branch,
  draft PR, never a change the owner has open.
- A heartbeat is a check-in, not a work tick: read-only plus memory curation.
- Calendar mutations, commits, pushes, and PR creation each require their own
  payload-bound approval. MCP calls currently require none — a known gap.
- Unprompted output is Telegram-only, stamped explicitly via
  `proactiveDestination()`, so quiet-hours and weekly-limit accounting stays
  meaningful.
- Durable context retains active-secret filtering. SOUL/USER/MEMORY are
  context, never capability grants.
- Config, state, context, and session stores keep file locking and atomic
  writes. `/data/state.json` stays compatible or gets a tested migration.
- Repository and stdio-MCP subprocesses keep root restrictions, environment
  allowlisting, timeouts, output limits, and process-group cancellation.
- Telegram keeps webhook authentication, owner allowlisting, and update
  deduplication.

**Process**
- Changes are developed test-first and verified with focused tests followed by
  `make fmt vet test race build`; `make smoke` when Docker is available.

---

## Sources

Comparative figures above were checked in July 2026, not recalled.

- [OpenClaw issue #39945 — Progressive Disclosure for Skills](https://github.com/openclaw/openclaw/issues/39945) — 83 skills, 41 visible, closed without maintainer action
- [OpenClaw 9-layer system prompt breakdown](https://youmind.com/landing/x-viral-articles/openclaw-agent-system-prompt-architecture) — layer sizes
- [openclaw/openclaw](https://github.com/openclaw/openclaw)
- [Anatomy of Pi, the minimal coding agent](https://shivamagarwal7.medium.com/agentic-ai-pi-anatomy-of-a-minimal-coding-agent-powering-openclaw-5ecd4dd6b440) — 4 tools, <1K-token prompt
- [Hermes Agent — Prompt Assembly](https://hermes-agent.nousresearch.com/docs/developer-guide/prompt-assembly/) — stable/context/volatile tiers, cache rationale
- [Hermes Agent — Architecture](https://hermes-agent.nousresearch.com/docs/developer-guide/architecture)
- [Anthropic — Advanced tool use](https://www.anthropic.com/engineering/advanced-tool-use) — Tool Search Tool, deferral thresholds
- [Claude Code — MCP docs](https://code.claude.com/docs/en/mcp)
