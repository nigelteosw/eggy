# Eggy roadmap

This file tracks **unfinished work and standing decisions only**. Completed
work lives in git; current behavior lives in `README.md` and
`docs/ARCHITECTURE.md`. Delete an item once it has landed — do not leave it
here as a record.

Standing decisions are the exception: they are here so a settled question is
not re-opened by accident, and each one names the condition that would justify
re-opening it.

---

## Where things stand

20,512 lines of production Go, 16,992 of test. 678 tests. 20 tools on an
ordinary owner turn (25 with Calendar, more with MCP). 17 top-level slash
commands. 10 config sections. `internal/kernel/services` holds 18 source files,
`internal/kernel/services/repo` another 12.

Per-turn context floor is **12,416 bytes (~3.1K tokens)** with empty durable
docs, no skills, Calendar and MCP off — tool schemas 7,855, runtime policy
3,360, manifest 542, SOUL/USER/MEMORY 495, temporal 132, skills index 32.
Tool schemas are the largest section by a wide margin; that is where the
ceiling is, not the prompt.

For scale, measured July 2026: Pi ships 4 tools and a sub-1K-token prompt;
OpenClaw's framework layers alone run 40–90KB and inject every skill's full
body. Eggy's context assembly is in good shape. **Do not optimize bytes.**

---

## P1: Close the MCP authorization gap

The only open safety item, and the one that blocks another decision below.

An MCP tool call is an arbitrary remote side effect gated by nothing but a
failure cooldown. Every other capability that reaches outside the process —
commit, push, pull-request creation, Calendar mutation — carries a
payload-bound approval. MCP carries none. Owner-prompted turns invoke MCP
tools ungated; the only mitigation is that unprompted turns cannot reach them
at all.

- [ ] Decide between two defensible answers, and implement whichever is
      chosen:
      - **Gate it.** An approval classification for MCP calls, payload-bound
        like the others. Needs a rule for which calls are gated — gating every
        read would make MCP useless, and the server does not tell us which of
        its tools mutate.
      - **State the trust.** Document that configured MCP servers are trusted
        the same way configured repositories are, and that `mcp.servers` is
        therefore a reviewed-configuration surface rather than a runtime one.

      The current state — an unstated assumption — is the one option that is
      not defensible. Whichever way this goes, update the standing constraint
      below, which currently records it as a known gap.

## P2: Verify the prompt-caching win empirically

The section reorder (stable prefix first) is correct by construction:
`openaicompat.Generate` maps each message 1:1 preserving order, so the stable
prefix reaches the wire intact. It has not been *measured*.

- [ ] Surface `ModelUsage.CachedPromptTokens` in `/usage`. It already decodes
      `prompt_tokens_details.cached_tokens`, so the hit rate is recorded today
      and simply not shown. Cheap, and it turns a construction argument into
      evidence.

## P2: Trim duplicated prose between policy and tool descriptions

- [ ] The runtime policy fragments and the tool descriptions say some of the
      same things twice. `memory` has a 551-byte description and the memory
      fragment re-explains the byte budget on top of it; `propose_change` has a
      414-byte description plus a fragment paragraph. Pick one home per fact.

## P2: Make MCP schema weight observable

- [ ] Add a `/context` line reporting MCP tool schema bytes separately from
      kernel tool schema bytes. This exists to make the deferral threshold
      below observable, not to reduce anything today.

## P2: Documentation weight

`README.md` (~300 lines) and `docs/ARCHITECTURE.md` (~490 lines) overlap
substantially, and code comments carry a third narrative — `loop.go` is heavy
on prose that litigates rejected alternatives.

- [ ] Rewrite `README.md` as a short operator's guide and let
      `docs/ARCHITECTURE.md` be the only narrative.
- [ ] Audit the rest of `AGENTS.md`'s port list. One entry referenced
      `services.Implementer` and a file that does not exist; others may be
      equally stale.
- [ ] When a comment explains *why an alternative was rejected*, move it to the
      commit message or `docs/ARCHITECTURE.md`. Trim on touch — do not do a
      documentation-only sweep.

## P2: Run recovery and rollback

- [ ] Continue an existing pull request on its own branch rather than branching
      from trunk and opening a duplicate.
- [ ] Resolve the pull request a continuation belongs to from the thread alone.
- [ ] Save a bounded patch and validation artifact before workspace cleanup.
- [ ] Cleanup and retention diagnostics for abandoned workspaces.
- [ ] An explicit discard operation that cannot affect the owner's checkout.

## P2: Other open work

- [ ] `/skills browse <repo-url>` and `/skills clone <repo-url> <path>`,
      read-only, no bulk importer.
- [ ] Reject duplicate, secret-like, prompt-injection, exfiltration, and
      invisible-Unicode content before durable context writes.
- [ ] Evaluate container-per-run isolation. Three subprocess surfaces
      (`terminal`, repository runs, stdio MCP children) execute as Eggy's own
      user with Eggy's own filesystem access. Each constructs a minimal
      environment; none is isolated. This is also the only real fix for the
      workspace path containment documented in `repo/workspace_path.go`.

## Operational follow-ups

Manual deployment steps, not code changes.

- [ ] Set `server.trusted_proxy_hops: 1` in Railway's `/data/config.yaml`. It
      defaults to 0, which behind Railway's proxy keys the web login throttle
      on the proxy's address for every request.
- [ ] Reset Railway's `/data/config.yaml` so the next boot generates the
      current config shape.

---

## Standing decisions

Settled. Each names what would justify re-opening it.

**Scope: almost everything stays.** (2026-07-28) The owner reviewed every
capability cluster against actual use. Web search was the only cut. Slash
commands (both grammars), MCP, web UI, Calendar, memory + embeddings,
shipping/changes/checks, scheduler/heartbeat, and skills all stay. The
complexity is therefore intrinsic to what Eggy is meant to do, not accidental,
and reduction cannot come from deletion — the work is containment. *Re-open if
a capability stops being used.*

**Calendar stays native.** The reason is availability: there is no widely
available Google Calendar MCP server, so native is the only option that
exists, not a preference. Two things matter if one appears. First, the line
count overstates the saving — of ~1,170 lines only ~535 are fungible Google
plumbing; the other ~240 in `services/calendar.go` are the safety envelope
(payload-bound approval, idempotency keys, ETag binding). Second, that
envelope has no MCP equivalent while the authorization gap above is open.
*Re-open only after that gap is closed, and only for a server that has proven
itself.*

**Do not build deferred MCP tool schemas yet.** Anthropic's guidance is to
defer when tool definitions exceed ~10K tokens and to skip deferral below ~10
tools, where search latency costs more than it saves. Eggy's 20 tools cost ~2K
tokens — a fifth of the threshold. *Re-open when MCP schemas alone exceed ~10K
tokens; the `/context` item above makes that observable.* The failure mode is
real once reached: 50+ MCP tools run 55–72K tokens upfront and deferral cuts
~85%.

**Do not split `internal/kernel/services` further.** The two-package split
(`services` + `services/repo`) followed the one seam that exists. A four-way
split was attempted on paper and abandoned after measurement: `decodeStrict`
was used by 9 files spanning three would-be packages, and `tools.go`'s status
tool created a genuine import cycle. `SecretGuard` is shared by context,
memory, skills, and transcripts today and would hit the same wall. *Do not
export internals to make a directory boundary possible — that trades
encapsulation for tidiness.* Measure cross-references before any further
attempt.

**Test fakes are duplicated across the two kernel packages** (`memoryStore`,
`memoryTranscriptStore`, `fakeShippingGateway`, `mustMarshal`, `webThread`). A
fake is not API, and an exported `kerneltest` package for forty-line stubs is
the worse trade. *Revisit if a third package needs them.*

**`internal/web`'s MCP config routes stay as a special case.** They are not a
boundary violation — `internal/web` imports `internal/config`, not the
adapter — and they back a real settings panel. *Genericize only when a second
config section needs the same treatment, at which point the generic route pays
for itself.*

**The Pi four-tool test applies to new tools only.** Before adding a tool, ask
whether the model could do it with `terminal` and a CLI. Applied retroactively
it would delete capabilities whose value is an approval envelope or a
credential Eggy holds rather than the API call itself.

**Migrations are gone and `/data` cannot be restored from before them.**
The boot migrations for legacy coding runs, Calendar credentials, schedules,
and per-server MCP OAuth files were removed once the deployment had been
through them. Restoring a `/data` backup predating any of them would strand
that data; recovery means checking out a commit before their removal, booting
once, then upgrading.

---

## Standing constraints

Properties every change must preserve.

**Architecture**
- `internal/kernel` and `internal/ports` stay provider-neutral. Adapters live
  under `plugins/` and are wired only in `internal/bootstrap`. No `internal/`
  package outside `internal/bootstrap` imports a plugin type.
- `services/repo` may import `services`; never the reverse.
- The primitive tool surface (`read_file`, `terminal`, `patch`, `write_file`)
  is kernel-owned and defined exactly once. Nothing shadows a primitive — the
  registry drops a provider tool that collides with a registered name.
- The loop has exactly one tool source. An adapter with a changing catalog is a
  provider on the registry, never a second source on the loop.
- Operational state is file-backed, so production runs exactly one `eggyd`.

**Safety**
- Nothing lands in a repository without payload-bound authorization and a
  human-reviewed pull request. Protected branches stay unpushable. Eggy never
  merges.
- Unprompted turns (scheduled, heartbeat) may only *propose*: isolated branch,
  draft PR, never a change the owner has open.
- A heartbeat is a check-in, not a work tick: read-only plus memory curation.
- A turn is never told about a capability it does not have. The runtime policy
  is assembled from `CapabilityManifest.Tools`, the same field the manifest
  renders.
- Calendar mutations, commits, pushes, and PR creation each require their own
  payload-bound approval. **MCP calls currently require none — a known gap,
  tracked above.**
- Unprompted output is Telegram-only, stamped explicitly via
  `proactiveDestination()`, so quiet-hours and weekly-limit accounting stays
  meaningful.
- Durable context retains active-secret filtering. SOUL/USER/MEMORY are
  context, never capability grants.
- Config, state, context, and session stores keep file locking and atomic
  writes. `/data/state.json` stays loadable or gets a schema-version change.
- Repository and stdio-MCP subprocesses keep root restrictions, environment
  allowlisting, timeouts, output limits, and process-group cancellation.
- Telegram keeps webhook authentication, owner allowlisting, and update
  deduplication.
- Workspace path containment is lexical and stops mistakes, not attackers.
  Configured repositories are trusted code running as Eggy's own user.

**Process**
- Changes are developed test-first and verified with focused tests followed by
  `make fmt vet test race build`; `make smoke` when Docker is available.

---

## Sources

Comparative figures were checked in July 2026, not recalled.

- [OpenClaw issue #39945 — Progressive Disclosure for Skills](https://github.com/openclaw/openclaw/issues/39945) — 83 skills, 41 visible, closed without maintainer action
- [OpenClaw 9-layer system prompt breakdown](https://youmind.com/landing/x-viral-articles/openclaw-agent-system-prompt-architecture) — layer sizes
- [openclaw/openclaw](https://github.com/openclaw/openclaw)
- [Anatomy of Pi, the minimal coding agent](https://shivamagarwal7.medium.com/agentic-ai-pi-anatomy-of-a-minimal-coding-agent-powering-openclaw-5ecd4dd6b440) — 4 tools, <1K-token prompt
- [Hermes Agent — Prompt Assembly](https://hermes-agent.nousresearch.com/docs/developer-guide/prompt-assembly/) — stable/context/volatile tiers, cache rationale
- [Anthropic — Advanced tool use](https://www.anthropic.com/engineering/advanced-tool-use) — Tool Search Tool, deferral thresholds
- [Claude Code — MCP docs](https://code.claude.com/docs/en/mcp)
