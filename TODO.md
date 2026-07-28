# Eggy roadmap — complexity reduction

This file tracks unfinished work only. Current behavior belongs in `README.md`
and `docs/ARCHITECTURE.md`; completed implementation history remains in git.

This revision replaces a feature roadmap with a reduction plan. Nothing below
adds a capability. Remove an item when its change and focused tests have
landed.

---

## Diagnosis

Measured on the current tree, not estimated.

**Size.** 20,591 lines of production Go, 16,856 of test (was 21,399 / 17,660
before web search was removed). 20 tools on an ordinary owner turn (25 with
Calendar, more with MCP). 17 top-level slash commands across ~40 catalog
paths. 10 config sections. `internal/kernel/services` holds 18 source files
and `internal/kernel/services/repo` another 12.

**Separation of concerns is not the problem.** `ports` / `kernel` / `plugins`
is a real boundary and it mostly holds. The loop (`agent/loop.go`, 287 lines)
is small and correct. What is out of hand is the *number* of concerns, and a
handful of specific leaks.

The scope review (below) settled that the concerns are wanted: all but one
capability stays. So the number is not coming down, and the work is to contain
what is there — kill the boundary violations, split the oversized package where
a real seam exists, and delete dead weight — rather than to remove features.

**Per-turn context floor: 12,416 bytes (~3.1K tokens)** with empty
SOUL/USER/MEMORY, no skills installed, Calendar off, MCP off. Re-measured
after the two P0 fixes landed; it was 13,507 before:

| section | bytes | |
| --- | ---: | --- |
| tool schemas (20 tools) | 7,855 | |
| hard runtime policy | 3,360 | was 4,451 — now conditional on the turn |
| capability manifest | 542 | |
| SOUL.md / USER.md / MEMORY.md | 495 | |
| temporal context | 132 | |
| skills index | 32 | |

With Calendar wired, real durable docs, and one MCP server this is comfortably
25–30KB before the owner types anything. Tool schemas are now the largest
section by a wide margin — that is where the ceiling is, not the prompt.

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
| **Eggy** | ~3.1K tokens | 20 | index + `skill_read` ✅ |
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

**Pi is the standard to hold new tools to, not a retrofit target.** Four tools
— `read`, `write`, `edit`, `bash` — and a sub-1,000-token system prompt. Mario
Zechner's stated reasoning: frontier models already understand coding agents
from RL training, so rather than adding specialized tools, trust the model to
invoke CLI utilities through bash. Eggy ships that exact primitive
(`terminal`) and adds 16 tools around it. `web_search` fails the test outright
and is the one cut. The rest survive it for a reason the raw count hides:
their value is an approval envelope or a credential Eggy holds, not the API
call. Apply the test to every *new* tool; do not apply it retroactively.

**Hermes validated the two P0 fixes above.** It assembles the prompt in three
ordered tiers — stable (identity, tool guidance, skills index), context
(project files), volatile (memory, profile, timestamp) — explicitly ordered so
the stable prefix survives provider-side prompt caching and volatile data at
the tail cannot invalidate it. Eggy's ordering did the opposite in one place;
that is now fixed.

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

## P0: Scope — SETTLED (2026-07-28)

The owner reviewed every capability cluster against actual use. The result is
that almost everything stays, and that is a legitimate answer: the complexity
is intrinsic to what Eggy is meant to do, not accidental. It also means the
reduction cannot come from deletion. Everything below this section is
containment and separation instead.

| cluster | LOC | decision |
| --- | ---: | --- |
| slash commands | 2,393 | **keep both grammars** — Telegram and CLI both used regularly |
| MCP | 1,816 | keep — the extension mechanism going forward |
| web UI + webchat | 1,345 | **keep** — a real surface, not just Telegram |
| calendar | ~1,170 | **keep, and keep it native** (see below) |
| memory + embeddings | 920 | **keep** — semantic recall is used |
| shipping / changes / checks | 935 | keep — core repository workflow |
| scheduler + heartbeat | 701 | keep |
| skills | 688 | keep |
| web search | 762 | **DELETED** ✅ |

### The one cut — DONE

Web search removed whole: `plugins/search/{tavily,searxng,googlecse}`,
`services/web_search_tool.go`, `bootstrap/web_search.go`,
`ports.WebSearcher`/`WebSearchRequest`/`WebSearchResult`, the `WebSearchConfig`
section with its defaults and validation, three `Secrets` fields, the bootstrap
wiring and `integrations` entry, and the `config.example.yaml`, `.env.example`,
README, and `docs/ARCHITECTURE.md` sections. Nothing left behind.

One test in the deleted config block was not actually about web search: it
asserted that marshaling a `Config` never emits a *resolved* secret, only the
environment-variable name. That property still binds every provider key, MCP
bearer token, and the GitHub token, so it was rewritten generically as
`TestMarshaledConfigNeverLeaksAResolvedSecret` rather than deleted with its
subject.

### Calendar stays native — do not move it to MCP

**The primary reason is availability: there is no widely available Google
Calendar MCP server.** Native is not a design preference here, it is the only
option that exists. An earlier draft of this file called Calendar "the
clearest case of something that should have been MCP from the start" — that
was wrong on the facts, not just on the trade-off.

Two things follow for whenever a credible Google Calendar MCP server does
appear, because the decision should be re-opened then rather than treated as
settled forever:

- The line count overstates the saving. Of Calendar's ~1,170 lines only ~535
  are fungible Google plumbing (API calls, OAuth, token encryption). The other
  ~240 in `services/calendar.go` are the safety envelope:
  `RequestCreate`/`Create`, `RequestUpdate`/`Update`, `RequestDelete`/`Delete`,
  `ExecuteApproved` — payload-bound owner approval, idempotency keys, and ETag
  binding so a mutation cannot silently clobber a change made elsewhere.
- That envelope has no MCP equivalent today. MCP tool calls are ungated (see
  "Close the MCP authorization gap"), so migrating before that gap is closed
  would let the agent create and delete calendar events with no confirmation.
  **Close the authorization gap first, then reconsider.**

`gcalcli` through `terminal` is not a third option: `terminal` is
workspace-scoped, so it needs an attached repository that calendar has nothing
to do with, and it is ungated too.

The Pi test ("could the model do this with `terminal` and a CLI?") is still
worth applying to *new* tools. It does not apply retroactively to a capability
whose value is the approval envelope rather than the API call.

## P1: MCP must be a plugin, not an agent modification — LANDED

No file under `internal/kernel` refers to MCP as anything but a comment
example, and no package under `internal/` imports `plugins/tools/mcp` except
`internal/bootstrap`, the composition root that is allowed to.

**The boundary violation.** `internal/commands` imported the adapter and
exposed `mcpadapter.ServerStatus` / `ProbeResult` through `MCPCommands`. It now
declares its own `ToolServerStatus` / `ToolServerProbe` — labelled strings and
a count, which is all the command layer ever rendered — and
`bootstrap.mcpCommands` maps the adapter onto them, the same way bootstrap
maps every other adapter.

**The nil-interface trap is gone rather than documented.** `commands.SetMCP`
was deleted outright; `commands.Options.MCP` already existed and was already
wired at construction, so the setter was pure redundancy. Its doc comment and
the duplicate warning at `bootstrap/mcp.go`'s call site are replaced by
`newMCPCommands`, which returns a true nil interface when no manager was
built. The hazard is now structurally impossible instead of described.

**The loop has one tool source again.** `agent.Loop.SetDynamicTools`, its
`dynamic` field, and its per-turn merge are deleted. `NewSelectedLoop` takes a
single `agent.ToolSource`, and `services.ToolRegistry` — already the thing
that owned tool identity — grew `AddProvider` for catalogs that change while
the process runs. Bootstrap wires `registry.AddProvider(app.mcp.Tools)`.

Live reload is preserved: providers are read on every `Tools()` call, so
`/mcp reload`, a reconnect, and a logout all still take effect on the next turn
with no restart. The alternative (restart-to-reload) would have been cheaper
to build but removes a capability, which the settled scope says not to do.

The precedence invariant moved with the merge, from the loop to the registry,
and is now tested where it lives: a provider tool can never shadow a registered
one, so the kernel primitives stay defined exactly once no matter what a remote
server advertises. The loop keeps a first-wins backstop for a source that hands
it duplicates anyway.

**The variadic handler hole is closed.** `NewHTTPHandlerAt`'s trailing
`...http.Handler` — which accepted any number of handlers and silently ignored
all but the first — is replaced by `web.NewHTTPHandler(web.Routes{...})` with a
named `MCPCallback` field. `NewHTTPHandlerAt` is gone; there is one constructor.

### Deliberately not done

- [ ] `internal/web`'s three MCP config routes and `config.SetMCPServer` /
      `RemoveMCPServer` / `GetMCPServersConfig` are left alone. They are a
      special case with no generic equivalent, but they are *not* a boundary
      violation — `internal/web` imports `internal/config`, not the adapter —
      and they back a real web settings panel. Genericizing them is a refactor
      with risk and no user-visible benefit; deleting them removes a feature
      the settled scope says to keep. Revisit only if a second config section
      needs the same treatment, at which point the generic route pays for
      itself.

MCP in the prompt needed no work: `hardRuntimePolicy`'s "reaches no MCP tool"
sentence left with the scheduled-turn rules when the policy was split, and the
tool list now expresses it.

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

## P1: Split `internal/kernel/services` — LANDED (partially)

29 files in one package became 18 in `services` plus 12 in
`services/repo` (repositories, workspaces, changes, shipping, checks, the
primitive tools, and the status tool). The dependency is strictly one-way:
`repo` imports `services`, never the reverse. 682 tests pass, unchanged in
count.

### The plan in this file was wrong, and checking it first is why it landed

An earlier revision claimed "the split is a rename plus import fixes; the
types are already separated, only the directory isn't." That was asserted, not
verified, and it was false. Measuring the actual cross-file references found
two blockers that a four-way split (`repo`/`context`/`schedule`/`diag`) could
not clear without making the package worse:

- `decodeStrict`, an unexported helper, was used by 9 files that would have
  landed in at least three different packages. A four-way split forces it
  public, or duplicated four times.
- `tools.go` held the `status` tool, which reads `Changes`. That is a
  `services` file depending on a `repo` type while `repo` files depend on
  `decodeStrict` in `services` — a genuine import cycle, invisible until you
  look.

So the split is **two packages, not four**, along the one seam that actually
exists. Clearing the blockers took two small preparatory changes, both worth
having on their own: `decodeStrict` is now the exported
`services.DecodeToolInput` (tools live in more than one package now, and all
of them must reject a malformed call identically), and the status tool moved
to `repo/status_tool.go`, which is where a tool reporting active runs and
configured repositories belonged anyway.

### What is left

- [ ] The remaining 18 files in `services` still cover several concerns
      (durable context, skills, calendar, approvals, agent runtime, heartbeat,
      dispatch, diagnostics, transcripts, turn plumbing). Splitting further
      hits the same wall: `SecretGuard` alone is shared by context, memory,
      skills, and transcripts. Do not attempt it without measuring the
      cross-references first, and do not export internals to make a directory
      boundary possible — that trades encapsulation for tidiness.
- [ ] Test fakes are duplicated between the two packages
      (`memoryStore`, `memoryTranscriptStore`, `fakeShippingGateway`,
      `mustMarshal`, `webThread`). This is deliberate — a fake is not API, and
      an exported `kerneltest` package for forty-line stubs is the worse
      trade — but if a third package appears, revisit.
- [ ] `AGENTS.md` referenced `services.Implementer` and
      `internal/kernel/services/implementer.go`, neither of which exists. Fixed
      in passing; the rest of that file's port list deserves the same audit.

## P1: Correctness and dead weight — LANDED

**Migrations removed** (owner-authorized). `legacy_coding_runs.go`,
`migrate_auth.go`, `migrate_cron.go`, and `mcp/oauth_migrate.go` are deleted
along with their boot call sites. All four were idempotent one-way moves into
the current layout, and the deployed instance has booted with them many times.

*Precondition, stated because removal is irreversible:* this is safe only
because `/data` has already been through them. Restoring a `/data` backup that
predates any of these migrations, on a build after this commit, would strand
that data — the Calendar credential in `state.json`, schedules in
`state.json`, per-server MCP OAuth files under `<home>/mcp/`, and pre-
unification `coding_runs`. Recovering would mean checking out a commit at or
before this one, booting once, then upgrading.

**Three dead `State` fields removed.** `RecentMessages` (replaced by the
SQLite conversation window), `Schedules` (replaced by one file per job under
`<home>/cron`), and `Calendar` (replaced by `auth.json`) existed only to be
migrated out of. `SchemaVersion` is 5. Loading is unaffected — the store uses a
plain `json.Unmarshal`, so a `state.json` still carrying those keys has them
ignored and dropped on the next write. `store_test.go` keeps its old-shape
fixtures and now asserts exactly that: a file written by an older Eggy still
loads, retired keys and all.

**One safety test followed its data instead of retiring with it.**
`TestHeartbeatIsIsolatedFromRecentConversationHistory` seeded a stale
instruction into `State.RecentMessages` and proved a heartbeat could not see
it. That field is gone; the property is not. It now seeds the durable
conversation store, which is where the ambient window actually comes from.
`assertNoDurableMessages` excludes the seed by content rather than by count,
so it cannot be satisfied by an off-by-one.

**Path containment comment corrected.** `resolveWorkspacePath` claimed "a
primitive can never touch a file outside the checkout the session is bound
to." It cannot promise that, and `EvalSymlinks` was deliberately *not* added:
the `terminal` tool hands an arbitrary command to `sh -c` in the same
workspace, so hardening the path join would advertise a guarantee the process
cannot keep. The comment now states the containment is lexical, says what it
does stop (a traversal mistake, not an attacker), and names the real threat
model — configured repositories are trusted code running as Eggy's own user —
pointing at process isolation as the only real fix.

**Web password documented as plaintext.** `README.md` now states that
`EGGY_UI_PASSWORD` is compared as a plaintext shared secret rather than a
hash, why that is defensible here (the same environment already holds the
provider keys, GitHub token, and encryption key), and the two conditions that
would invalidate it.

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
