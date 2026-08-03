# Eggy cleanup plan

A plan for one thing: **separation of concerns and code cleanup.** Product
direction, new capabilities, and deployment chores are parked at the bottom
rather than mixed in here.

Unfinished work only. Completed work lives in git; current behavior lives in
`README.md` and `docs/src/content/docs/`. Delete an item once it lands.

The bar every item clears: **it deletes or moves code.** An item that only adds
code needs an argument for why the thing it prevents is worse than the thing it
costs. More code is harder to maintain, so a refactor that nets larger is a
refactor that failed.

## Design rules this plan enforces

- **A capability that is not configured costs nothing at runtime** — no tool
  schema, no goroutine, no store, no HTTP route, no prompt bytes.
- **One way to do a job.** A second implementation of something that already
  exists is the defect this plan exists to find.
- **Boundaries are one-way.** `internal/kernel` and `internal/ports` stay
  provider-neutral; adapters live under `plugins/<category>/<provider>` and are
  wired only in `internal/bootstrap`; the direction is
  `config <- web <- bootstrap`.

## Baseline (re-measured 2026-07-31, after the bootstrap pass)

Non-test `.go` outside `docs/` and `website/`:

- **15,755 production lines**, 13,184 test lines;
- largest packages: `plugins/tools/mcp` 1,634, `internal/config` 1,561,
  `internal/bootstrap` 1,469, `internal/kernel/services` 1,260, `internal/web`
  911, `internal/commands` 770;
- **75 YAML-tagged fields** in `internal/config/config.go`;
- machine-managed persistence spans `state.json`, `auth.json`, `cron/`, and
  `eggy.db`; Markdown context and skills remain files by design.

Bootstrap fell 1,524 → 1,469 while `internal/kernel/services` rose 1,097 →
1,260: the tools moved rather than vanished, which is what that pass was for.
Production lines still rose overall (15,622 → 15,755), because every pass so
far has traded scattered code for named code plus the comments explaining it.
Re-measure before claiming progress against the targets, not from memory.

## Targets

- **three durable forms**: YAML for startup config, Markdown for owner-facing
  documents, SQLite for everything machine-managed.
- **one runtime administration authority** — every config write goes through
  `internal/config` under one lock; Telegram and web are views onto it.

The **≤ 12,500 production lines** and **≤ 50 config fields** targets are
retired. Both counted the tree rather than what a turn costs to run, and both
were moving away — three cleanup passes each netted slightly larger, because
moving code where it belongs costs naming and comments. Neither was reachable
without deleting a capability, which is a product decision to argue on its own
merits rather than smuggle in as a line-count exercise.

---

## Config surface: closed

The document duality is gone (`Config` carries its own YAML tags). The other
four items in this section were investigated and **closed without work** — see
"Decided" below for the field-count target, `enabled` flags, the unknown-key
message, and `config.example.yaml` coverage.

Nothing in `internal/config` is currently a cleanup item.

---

## P2: Mechanical cleanups

- [ ] 23 non-test `sort.Strings`/`sort.Slice` calls predate the Go 1.26
      baseline and read as `slices.Sort`/`slices.SortFunc`. Do it when a change
      already touches those files, not as its own churn commit.
- [ ] Add two characterization tests to `plugins/tools/google`: that
      `access_type=offline` + `prompt=consent` reach the authorization URL, and
      that a mismatched state is rejected. MCP pins both; Google pins neither,
      and both failures are silent — a grant that stops renewing weeks later.
- [ ] Decide whether `Endpoints` stays in-package in `plugins/tools/google`. It
      is settable only by tests today and must never become config: an
      operator-settable API host is a credential exfiltration primitive.
- [ ] `docs/superpowers/specs/2026-07-31-compact-docs-sidebar-design.md` shipped
      without the regression test it requires. `docs/tests/navigation.test.ts`
      covers routes, content coverage, and previous/next only, so neither
      property that design exists to protect is pinned: the desktop sidebar
      being non-scrollable, and the brand mark using `--brand-yellow` rather
      than the mint accent. The second is one CSS variable away from silently
      regressing into exactly the failure the spec was written to prevent.
- [ ] Nothing pins the documented Telegram commands against `internal/commands`.
      The docs-site spec made it a one-time manual verification step, so the
      five shipped commands and their documentation can drift apart silently.
      `navigation.test.ts` already does this class of check for routes.

---

## Decided — do not re-propose

**The two OAuth flows stay separate.** Unifying `plugins/tools/google/oauth.go`
and `plugins/tools/mcp/oauth.go` behind one parameterized adapter was
considered and rejected. Shared: state and verifier generation, the pending
window check, the exchange, token persistence — 50-60 lines. Not shared, and
structural, all on the MCP side: protected-resource discovery,
authorization-server metadata with trailing-slash tolerance and synthesized
fallback endpoints, RFC 7591 dynamic registration, a `resource` parameter
threaded through every call, per-server keying, and conformance to the MCP
SDK's `auth.OAuthHandler`. Google has fixed endpoints and one loopback
constant. One adapter would carry all of that past a provider that never uses
it, adding branches to delete ~50 lines. Revisit only if a *third* provider
appears on the MCP side of the split.

**`GoogleRuntime` and `MCPRuntime` stay separate.** Four lines each; collapsing
them needs a server key Google ignores or a named-grant registry. Machinery to
delete eight lines.

**`parseOAuthRedirect` stays in `internal/commands`.** Its header calls itself
homeless, but both callers *are* the command surface, and moving it would have
two plugin packages import a third.

**Owner authentication stays separate from outbound authorization.**
`plugins/auth/session` answers "who may talk to Eggy"; the OAuth grants under
`plugins/tools/*` answer "what may Eggy do on the owner's behalf". They point
in opposite directions of trust, and one "auth" package owning both is a
security god-object. Telegram's webhook-secret and owner-allowlist checks are
the only things that may later join `session`.

**Tool registration stays inline in `NewApp`.** Extracting a
`buildToolRegistry` helper was tried and rejected: it needs eleven
collaborators, so it trades a long function for an eleven-parameter one and
hides the coupling instead of removing it. It becomes worth revisiting only if
what a turn's tool set is assembled from gets smaller.

**Adding a model backend is usually config, not code.** `openai_compatible` is
OpenAI's Chat Completions wire format — `/chat/completions`, `tools` /
`tool_calls`, `reasoning_effort`, `usage.cached_tokens` — so OpenAI itself,
DeepSeek, OpenRouter, and Groq are all reachable by adding a `providers` entry
with the right `base_url` and `api_key_env`. Writing an `openai` adapter that
duplicates `openaicompat` is the wrong turn, and a test pins that. A new
adapter is earned by a genuinely different wire format: Anthropic's Messages
API, with its top-level system prompt, content blocks, and required
`max_tokens`, is the real example. That costs a plugin package, a name in
`config.supportedModelAdapters`, and one case in `bootstrap.newModelAdapter`.

**The 75 config fields are not a cleanup target.** They are not redundant: 20
are MCP server options (transport, auth, OAuth client, timeouts, failure
policy, tool filters), 7 are Google, and the rest are server, runner, and
repository settings that each do something. None is a second way to do a job.
Cutting to 50 means removing a capability or removing knobs from MCP — and
MCP's knobs are what let capability arrive as configuration instead of code,
which is the property the whole design is built on.

**`enabled` flags stay on MCP servers and Google.** The rule "an empty section
is the off switch" does not hold when the section carries state that is
expensive to recreate. An MCP server entry holds a URL, auth mode, OAuth client
id, timeouts, and tool filters; Google holds a client id and product list.
`enabled: false` turns the capability off while keeping all of it, which
deleting the section does not. The MCP manager also reads `Enabled` at runtime
to gate reconnection, so the field is load-bearing rather than declarative.
Removing it would break every existing config (`KnownFields(true)` rejects the
now-unknown key) and cost migration code to fix.

**The unknown-key message is already a repair.** `KnownFields(true)` reports
the line, the key, and the section type: `line 34: field enabld not found in
type config.GoogleConfig`. Adding a "did you mean" suggestion needs edit
distance over yaml tags plus mapping a type name in an error string back to a
`reflect.Type` — real machinery for a message that already names the file
position and the exact key.

**`config.example.yaml` stays a starter, not a reference.** Asserting it
documents all 75 fields would force the advanced MCP options, tool filters, and
failure policy into a file whose job is to get someone running. Field reference
belongs on the docs site. `TestLoadConfigAcceptsExample` already pins that the
example parses and produces the expected values, which is the property worth
holding.

**`knownGoogleProducts` stays duplicated in `internal/config`.** The boundary
rule forces it: config may not import a plugin package. If a third copy
appears, invert it — let the adapter validate its own product names and have
config check only the shape.

### Invariants that cost a defect to learn

- Google product names are canonicalized once, in `config.applyDefaults`.
  Validation accepts any casing, so when scope selection matched exactly and
  the adapter matched case-insensitively, `products: ["Gmail"]` registered the
  tool and requested no scope — every call 403'd, reading as a broken API.
- `Secrets.Values()` is the only list of live credentials. Bootstrap built a
  second one for the durable-context secret guard and it drifted: the Google
  client secret and the MCP OAuth client secrets were absent, so those two
  alone could reach `MEMORY.md` and recall unmasked. A reflection test now
  fails when a field is added to `Secrets` but not to `Values`.
- Either OAuth flow must keep: forced consent so a refresh token is really
  issued, granted-scope recording, the pending window, the loopback redirect
  matching byte for byte, and per-record associated data binding a grant to the
  server it was issued for.

---

## Capability roadmap

Not cleanup. This section is the product half of the file, kept here so it is
not lost, and ordered. Every item still states a deletion budget.

### Where it came from

Read against two comparable projects, from their primary sources on 2026-08-03
rather than from summaries: **OpenClaw** (`github.com/openclaw/openclaw`,
`docs.openclaw.ai`) — a self-hosted gateway fronting ~10 chat channels with
per-agent/per-sender session isolation, node pairing, an on-demand plugin and
skill marketplace, and paired iOS/Android companion nodes for voice and camera;
and **Hermes Agent** (`github.com/NousResearch/hermes-agent`, its `AGENTS.md`) —
one agent core behind a CLI, TUI, desktop app, and a ~20-platform gateway, with
autonomously authored self-improving skills, pluggable memory providers, 40+
built-in tools across seven terminal backends, and `delegate_task` subagents.

Both are broader than Eggy and neither is a target. Eggy is one owner, one
process, one replica; most of what they have is surface Eggy has deliberately
declined. What is worth taking is listed below; what is not is in **Rejected
from the comparison**, so it does not get re-proposed every time someone reads
their READMEs.

**Already matched, do not rebuild:** an adapter-per-channel port
(`ports.Channel` + `TrackableChannel`/`TypingChannel`), MCP as the extension
path, cron plus a silence-permitted heartbeat, FTS5 conversation search,
owner-editable Markdown context, config-not-code model providers, and an
approval mechanism with payload binding.

**Adopt as a rule, not as code — Hermes' footprint ladder.** Before a
capability becomes a core tool, try, in order: extend existing code; a command
plus a skill; a tool gated off when unconfigured; an MCP server. Core tool is
last, because every core tool ships on every API call. This is the same
principle as this file's "a capability that is not configured costs nothing at
runtime", stated as a decision procedure, and it decides R2 below on its own.

### R1 — Approval-gated MCP tools

The open safety gap, named in `AGENTS.md` and left by deleting Calendar: a
configured MCP server is trusted wholesale, so a calendar or mail mutation
arriving over MCP carries no per-call approval, while the same class of action
through a native tool would. Both references gate sensitive operations
(OpenClaw pairing/allowlists, Hermes `clarify`/`sudo`); Eggy has the machinery
already and simply does not reach it from the MCP path.

A per-server `require_approval: [tool names]` list routing a matching call
through the existing approval flow, with one `approvals.Action` and one
executor, per the standing safety rule.

Deletion budget: +~60 production lines, +1 config key per MCP server, 0 tools,
0 durable records, 0 background loops, 0 ports changes.

### R2 — Web reach

Eggy has no way to read the open web. Everything it knows arrives from the
model's weights, the owner, a configured Google product, or a configured MCP
server. For a personal agent that is a real hole, not a stylistic one.

The footprint ladder answers it: **this is an MCP server, not a core tool.** A
fetch/search tool in `internal/kernel` would ship its schema on every API call
for every owner, including ones who never want Eggy reaching the internet. The
work is therefore configuration and documentation, not Go: pick a fetch/search
server, verify it against R1's approval list, and write it up in
`config.example.yaml` and the docs site. If it turns out no server is
acceptable, that is the argument for a core tool — make it explicitly.

Deletion budget: 0 production lines, 0 config fields (an entry in the existing
`mcp.servers` map), 0 tools, 0 durable records, 0 background loops.

### R3 — Voice in

Both references transcribe voice memos, and both are right to: the owner
surface is a phone, and dictating is the natural input there. Eggy handles no
audio at all today — `plugins/channels/telegram` ignores a voice message.

This is ingest, not a tool: a narrow `Transcriber` port, one adapter, and the
Telegram webhook turning a voice update into ordinary user text before it ever
reaches the loop. It adds no tool schema and no prompt bytes, which is why it
is cheap despite being new capability. Voice *out* is not part of this.

Deletion budget: +1 narrow port, +1 plugin package (~150 lines), +2 config
keys, 0 tools, 0 durable records, 0 background loops.

### R4 — Agent-authored skills

`ports.SkillsStore` already has `Write` and `Delete`; `plugins/skills` already
implements and validates them; the web panel already reaches them. The kernel
exposes `skill_read` and nothing else, so the agent can consume procedural
memory but never record it. Hermes' entire self-improvement claim rests on
closing exactly that loop — skills authored after a complex task, refined in
use — and Eggy is one tool away from the same thing with no new port, no new
store, and no new durable form.

Two caveats that are the actual work, not the tool:

- A skill the agent wrote is prompt-injection persistence. Anything reaching
  disk must pass the same `services.NewSecretGuard(activeSecrets)` filter the
  durable context documents already pass, and the write path must be pinned by
  a test the way `Secrets.Values()` is.
- Skills grow without bound and stale ones cost context on every turn, since
  summaries are always resident. Hermes needed a background curator; Eggy
  should instead cap the resident summary list and let the owner prune from the
  web panel — a bound, not a loop.

Deletion budget: +2 tools, +~80 production lines, 0 config keys, 0 new ports,
0 new durable records, 0 background loops.

### R5 — An Anthropic Messages adapter

The one model backend that is genuinely earned rather than a `providers` entry
(see "Adding a model backend is usually config, not code"). Different wire
format: top-level system prompt, content blocks, required `max_tokens`.
Roadmapped rather than merely decided, because until it exists every claim that
Eggy is provider-neutral rests on one adapter and a switch statement with a
single case — `newModelAdapter`'s `default` branch has never been exercised by
a real second implementation.

Deletion budget: +1 plugin package, +1 name in `config.supportedModelAdapters`,
+1 case in `bootstrap.newModelAdapter`, 0 config fields, 0 tools, 0 ports
changes.

### R6 — Pin the prompt cache

Hermes treats a byte-stable cached prefix as an invariant and refuses
mid-conversation toolset mutation to protect it; it defers even skill installs
to the next session for the same reason. Eggy already *measures* the thing —
`ModelUsage.CachedPromptTokens` — but nothing pins it, and the MCP manager
reconnects at runtime, which can change the tool set inside a live
conversation. That is a cache invalidation the owner pays for silently, and it
is a live property of today's code rather than a hypothetical.

Establish the rule, then pin it: a test that the rendered system prompt is byte
stable across two turns with unchanged config, and a decision on whether an MCP
reconnection may alter the tool set mid-conversation or must wait. Absorbs the
old prompt-and-tool-budget item: re-measure the per-turn floor, report MCP
schema bytes separately from kernel schema bytes, and do not build deferred
tool loading until MCP schemas alone exceed ~10K tokens.

Deletion budget: +2 tests, ~0 production lines, 0 config keys, 0 tools.

### R7 — Does Eggy write code?

Still undecided between read-only inspection and a bounded edit tool plus
bounded shell. Both defensible, not compatible. The comparison does not settle
it: Hermes ships seven terminal backends and treats the shell as the point,
OpenClaw runs tools host-side by default with sandboxing optional. Eggy's
answer has to come from its own threat model, not theirs. Until decided, do not
reintroduce write tools opportunistically.

If it becomes yes, the standing rule holds without exception: independent
payload-bound approvals and protected-branch denial are mandatory, and no
commit, push, or pull-request capability arrives as a side effect.

### R8 — Decide the sandbox question out loud

A standing constraint already says configured repositories and stdio MCP
servers are trusted code running as Eggy's user, and that timeouts and
environment allowlists are not a sandbox. Both references offer isolation —
Hermes containers and remote terminal backends, OpenClaw optional tool
sandboxing — so "we trust them" now reads as an omission unless it is argued.

Argue it in `docs/src/content/docs/project/architecture.md`: single owner,
single process, one replica, configuration reachable only by the owner. Then
either keep trusting them on the record, or scope what a sandbox would cost.
Do not build one first.

Deletion budget: 0 production lines. This is a documentation item.

### R9 — Session recall beyond keywords

Hermes pairs FTS5 with LLM summarization for cross-session recall and a
persistent user model. Eggy has the FTS5 half and `memories/USER.md` as a
hand-curated stand-in for the other. Lowest priority on this list, and gated on
evidence: it earns a slot only after a recorded instance of `recall_conversation`
failing on a question the owner actually asked. Measure before building — an
embedding store is a new durable form, and the file has exactly three.

---

## Chores

Not roadmap, not cleanup. Tracked so they are not lost.

- **Consolidate durable state in SQLite** — move approvals, processed event
  IDs, selected model, and schedules out of `state.json`/`cron/` behind the
  existing ports; keep the Markdown files as files. Needs a schema-versioned,
  retry-safe migration design written first, and preserves encrypted payloads.
  The largest net-new-code item on the list, which is why it is parked.
- **Docs pass** — `README.md` becomes a short operator guide;
  `docs/src/content/docs/project/architecture.md` is the only architectural
  narrative. Audit `AGENTS.md`, the docs site, and `config.example.yaml` after
  every phase.
- **Railway operations** — set `server.trusted_proxy_hops: 1`; reset
  `/data/config.yaml` to the current shape before the next deploy (retired
  `scheduler`, `embeddings`, `implementation_sessions`, `calendar` sections);
  remove the `calendar` MCP server, which never authorized; keep
  `GOOGLE_CLIENT_SECRET` set to the **Desktop** client's secret.

---

## Rejected from the comparison — do not re-propose

Each of these is something OpenClaw or Hermes has and Eggy will not build.
Recorded with the reason so re-reading their READMEs does not restart the
argument.

**Channel breadth.** Both front 10-20+ messaging platforms. Eggy has one owner,
who is reachable on Telegram and on the web. A tenth channel does not let the
owner do anything an existing one does not — it multiplies adapters, webhook
authentication, deduplication ledgers, and approval-delivery paths for zero new
capability. `ports.Channel` stays the extension point if that ever changes;
adding a channel should remain a plugin package plus one line of bootstrap, and
that property is worth more than any particular channel.

**Multi-agent routing and per-sender session isolation.** OpenClaw isolates
sessions per agent, workspace, and sender because it serves groups. Eggy is
single-owner by definition — every message is from the same person — so the
routing key has one value and the isolation protects nothing.

**Subagent delegation (`delegate_task`).** Hermes spawns isolated subagents
with spawn-depth and concurrency bounds, an orchestrator/leaf role split, and
an async-completion queue re-entering the parent conversation. That is an agent
framework, which "Do not introduce" forbids by name, and it needs a second loop,
which the standing architecture rule forbids by name. It is also the single
largest net-new-code item either reference has. If parallelism is ever needed,
the answer is a scheduled turn, not a child loop.

**A plugin marketplace or hub.** ClawHub, Skills Hub, `hermes skills install
official/<category>/<name>` — an on-demand installer for third-party skills and
plugins. Eggy's skills are reviewed files placed by the owner in `skills/`, and
that review is the security model. An installer that fetches agent-readable
instructions from the internet deletes it. R4 lets the *agent* write skills into
the owner's own directory, which is a different trust boundary from fetching a
stranger's.

**Runtime-loaded plugins.** Both discover plugins from directories and entry
points at runtime. `Do not introduce` already names a runtime-loaded native
plugin system; Go's build model makes it worse than it is for Python or Node.
Compile-time wiring in `internal/bootstrap` plus MCP for out-of-tree capability
covers the same ground.

**A TUI, a desktop app, or companion nodes.** Hermes ships a TUI and an
Electron app; OpenClaw pairs iOS and Android nodes for Canvas, camera, and
voice. Each is a whole client to maintain. Eggy's surfaces are a phone the
owner already has and a browser, and production stays a single `eggyd` process.

**Pluggable memory providers.** Hermes plugs honcho, mem0, and supermemory in
behind an ABC — and its own May 2026 policy now refuses new in-tree ones,
because the maintenance burden is what a provider interface actually buys. Eggy
has three durable forms on purpose. A memory provider port would be a second
way to do a job that SQLite and Markdown already do.

**Profiles / multi-instance isolation.** Hermes isolates instances by
`HERMES_HOME` with per-profile token locks. Eggy has one home, one owner, one
replica; `EGGY_CONFIG` already relocates it for tests and local runs.

---

## Standing constraints

### Architecture

- `internal/kernel` and `internal/ports` remain provider-neutral.
- Adapters live under `plugins/<category>/<provider>` and are wired only in
  `internal/bootstrap`.
- `internal/kernel/services/repo` may import `internal/kernel/services`; never
  the reverse.
- The loop has exactly one tool source. Dynamic adapter catalogs are providers
  on the kernel registry, never a second loop or registry.
- Production remains a single `eggyd` process and one replica.

### Do not introduce

- a runtime-loaded native plugin system;
- a DI, agent, web, or ORM framework;
- build tags for ordinary feature selection;
- package splits that export internals only to satisfy directory boundaries;
- generic feature/module lifecycle interfaces whose only caller is bootstrap;
- a second way to do a job that already has one.

### Safety

- Telegram keeps webhook authentication, owner allowlisting, and update
  deduplication.
- Any protected mutation retains an independent payload-bound approval, with
  one `approvals.Action` and one executor per operation. Consolidating tools
  never consolidates their approvals.
- Generic Telegram selections can never satisfy an approval, authorize a
  mutation, or be read as approve/reject.
- Eggy has no repository commit, push, pull-request, or merge capability. If
  any returns, independent payload-bound approvals and protected-branch denial
  are mandatory.
- Unprompted turns cannot use MCP or mutate anything.
- Durable context retains active-secret filtering.
- Config and owner-facing files retain locking and atomic writes.
- Configured repositories and stdio MCP servers are trusted code running as
  Eggy's user; timeouts and environment allowlists are not a sandbox.

### Process

- Change behavior test-first and run the focused test before the full suite.
- Run `make fmt vet test race build` before completing each phase.
- Run `make smoke` when Docker is available; report an unavailable Docker
  daemon as an environment blocker, not a passing smoke test.
- Every feature proposal states its deletion budget: production lines, config
  keys, tools, durable records, background loops.
