# Eggy TODO

Unfinished work only. Delete an item once it lands.

Everything durable lives elsewhere and is not restated here: engineering rules,
safety invariants, settled decisions, and declined capabilities are in
`AGENTS.md`; current behavior is in `README.md` and `docs/src/content/docs/`;
completed work is in git.

Every item states what it costs. An item that only adds code needs an argument
for why the thing it prevents is worse than the thing it costs.

## Capability roadmap

Ordered. Each states a deletion budget, per the rule in `AGENTS.md`.

Read on 2026-08-03 against two comparable projects, from their primary sources
rather than summaries: **OpenClaw** (`github.com/openclaw/openclaw`,
`docs.openclaw.ai`) and **Hermes Agent** (`github.com/NousResearch/hermes-agent`).
Both are broader than Eggy and neither is a target. What was worth taking is
below; what was not is in `AGENTS.md` under "Declined capabilities".

Already matched, do not rebuild: an adapter-per-channel port, MCP as the
extension path, cron plus a silence-permitted heartbeat, FTS5 conversation
search, owner-editable Markdown context, config-not-code model providers, and an
approval mechanism with payload binding.

### R3 — Voice in

Both references transcribe voice memos, and both are right to: the owner surface
is a phone, and dictating is the natural input there. Eggy handles no audio at
all today — `plugins/channels/telegram` ignores a voice message.

This is ingest, not a tool: a narrow `Transcriber` port, one adapter, and the
Telegram webhook turning a voice update into ordinary user text before it ever
reaches the loop. It adds no tool schema and no prompt bytes, which is why it is
cheap despite being new capability. Voice *out* is not part of this.

Deletion budget: +1 narrow port, +1 plugin package (~150 lines), +2 config keys,
0 tools, 0 durable records, 0 background loops.

### R4 — Agent-authored skills

`ports.SkillsStore` already has `Write` and `Delete`; `plugins/skills` already
implements and validates them; the web panel already reaches them. The kernel
exposes `skill_read` and nothing else, so the agent can consume procedural
memory but never record it. Eggy is one tool away from closing that loop with no
new port, no new store, and no new durable form.

Two caveats that are the actual work, not the tool:

- A skill the agent wrote is prompt-injection persistence. Anything reaching
  disk must pass the same `services.NewSecretGuard(activeSecrets)` filter the
  durable context documents already pass, and the write path must be pinned by a
  test the way `Secrets.Values()` is.
- Skills grow without bound and stale ones cost context on every turn, since
  summaries are always resident. Cap the resident summary list and let the owner
  prune from the web panel — a bound, not a background curator loop.

Deletion budget: +2 tools, +~80 production lines, 0 config keys, 0 new ports,
0 new durable records, 0 background loops.

### R5 — An Anthropic Messages adapter

The one model backend that is genuinely earned rather than a `providers` entry.
Different wire format: top-level system prompt, content blocks, required
`max_tokens`. Roadmapped rather than merely decided, because until it exists
every claim that Eggy is provider-neutral rests on one adapter and a switch
statement with a single case — `newModelAdapter`'s `default` branch has never
been exercised by a real second implementation.

Deletion budget: +1 plugin package, +1 name in `config.supportedModelAdapters`,
+1 case in `bootstrap.newModelAdapter`, 0 config fields, 0 tools, 0 ports
changes.

### R6 — Pin the prompt cache

Eggy already *measures* the thing — `ModelUsage.CachedPromptTokens` — but nothing
pins it, and the MCP manager reconnects at runtime, which can change the tool set
inside a live conversation. That is a cache invalidation the owner pays for
silently, and it is a live property of today's code rather than a hypothetical.

Establish the rule, then pin it: a test that the rendered system prompt is byte
stable across two turns with unchanged config, and a decision on whether an MCP
reconnection may alter the tool set mid-conversation or must wait. Absorbs the
old prompt-and-tool-budget item: re-measure the per-turn floor, report MCP schema
bytes separately from kernel schema bytes, and do not build deferred tool loading
until MCP schemas alone exceed ~10K tokens.

Deletion budget: +2 tests, ~0 production lines, 0 config keys, 0 tools.

### R7 — Does Eggy write code?

Still undecided between read-only inspection and a bounded edit tool plus bounded
shell. Both defensible, not compatible. The comparison does not settle it: Hermes
ships seven terminal backends and treats the shell as the point, OpenClaw runs
tools host-side by default with sandboxing optional. Eggy's answer has to come
from its own threat model, not theirs. Until decided, do not reintroduce write
tools opportunistically — and note that a write-capable MCP server answers this
question by configuration, so it is the same decision wearing a different hat.

The safety invariant in `AGENTS.md` binds either way: if it becomes yes,
independent payload-bound approvals and protected-branch denial are mandatory,
and no commit, push, or pull-request capability arrives as a side effect.
Protected-branch denial is currently unbuilt — `ProtectedBranches` is validated
at config load and reported by `repository_list`, but nothing enforces it,
because there is no write path to enforce against.

### R8 — Decide the sandbox question out loud

`AGENTS.md` says configured repositories and stdio MCP servers are trusted code
running as Eggy's user, and that timeouts and environment allowlists are not a
sandbox. Both references offer isolation — Hermes containers and remote terminal
backends, OpenClaw optional tool sandboxing — so "we trust them" now reads as an
omission unless it is argued.

Argue it in `docs/src/content/docs/project/architecture.md`: single owner, single
process, one replica, configuration reachable only by the owner. Then either keep
trusting them on the record, or scope what a sandbox would cost. Do not build one
first.

Deletion budget: 0 production lines. This is a documentation item.

### R9 — Session recall beyond keywords

Hermes pairs FTS5 with LLM summarization for cross-session recall and a
persistent user model. Eggy has the FTS5 half and `memories/USER.md` as a
hand-curated stand-in for the other. Lowest priority on this list, and gated on
evidence: it earns a slot only after a recorded instance of
`recall_conversation` failing on a question the owner actually asked. Measure
before building — an embedding store is a new durable form, and there are
exactly three.

## Chores

- **Consolidate durable state in SQLite** — move approvals, processed event IDs,
  selected model, and schedules out of `state.json`/`cron/` behind the existing
  ports; keep the Markdown files as files. Needs a schema-versioned, retry-safe
  migration design written first, and preserves encrypted payloads. The largest
  net-new-code item on the list, which is why it is parked.
- **Docs pass** — `README.md` becomes a short operator guide;
  `docs/src/content/docs/project/architecture.md` is the only architectural
  narrative. Audit `AGENTS.md`, the docs site, and `config.example.yaml` after
  every phase.
- **Railway operations** — set `server.trusted_proxy_hops: 1`; reset
  `/data/config.yaml` to the current shape before the next deploy (retired
  `scheduler`, `embeddings`, `implementation_sessions`, `calendar` sections);
  remove the `calendar` MCP server, which never authorized; keep
  `GOOGLE_CLIENT_SECRET` set to the **Desktop** client's secret.
