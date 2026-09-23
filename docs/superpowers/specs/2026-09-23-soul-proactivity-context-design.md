# Soul, proactivity, and a self-aware prompt

Status: phase 1 implemented. Amended during implementation:
- **Heartbeat on/off switch.** The heartbeat became a per-account switch, off
  by default (`/heartbeat on|off`, and a switch in the panel's personal
  settings), stored in `ports.AgentRuntimeState` beside the thinking toggle.
  The runtime block's heartbeat line reaches only an account that switched it
  on.
- **`data_dir` defaults to the home.** An unset `data_dir` now defaults to the
  directory holding `config.yaml` (and a generated config writes that
  directory) rather than a fixed `/data`. Without that, `~/.eggy` as the
  default home would still have put SOUL.md in `/data`.
- **Soul reset needs no DELETE route.** Saving an empty soul resets it,
  because a blank file reads as the built-in soul.

Original status: design for review. No implementation yet. Scope held deliberately small:
Eggy is a minimal harness, so every item below extends code that already exists
and none adds a tool, a loop, or a durable record.

## Intended outcome

1. SOUL.md is Eggy's one personality setting. It lives at `<home>/SOUL.md`
   (`~/.eggy/SOUL.md` on a local run), shows up as a settings card in the web
   panel, is readable from Telegram, and Eggy can rewrite it itself when the
   owner asks it to change how it behaves — as Hermes does.
2. Eggy maintains its own watch list without being asked, so the heartbeat has
   something to check before the owner thinks to set one up. Eggy acts more
   like a PA: it notices what the owner is waiting on and follows up.
3. The prompt tells Eggy what it is and what it can do — surfaces, heartbeat,
   whose Google account it holds — in a handful of generated lines, while the
   prompt as a whole gets smaller.
4. A missing, emptied, or unreadable owner document falls back to a built-in
   default instead of failing the turn, and built-in defaults stay defaults
   rather than being copied into owner files.

## Current evidence

- `plugins/context/markdown/store.go`: SOUL.md is one shared file at the top of
  the home. `writableDocument` refuses it, and the kernel's `memory` tool offers
  only `memory`, `user`, and `watch`. `internal/web/watch.go:20` records that
  SOUL "is edited outside Eggy": there is no panel route and no Telegram
  command for it.
- `loadDocumentUnlocked` writes `initialSoul` into the file the first time it
  is read. From then on, a change to the built-in soul never reaches that
  deployment. An owner who empties the file gets an empty identity, and an
  unreadable SOUL.md makes `Load` fail, which fails every turn.
- `internal/home/home.go:34`: `DefaultRoot = "/data"`. The Dockerfile already
  sets `EGGY_HOME=/data`, so the default only matters for local runs, where
  `/data` is the wrong place.
- The heartbeat (`internal/bootstrap/heartbeat.go`) needs `heartbeat.interval`,
  a Telegram channel, and a non-empty WATCH.md before it beats. The watch list
  starts empty, and nothing tells an owner turn that the heartbeat exists, so
  the agent never adds to it without being asked.
- **`heartbeat_respond` ships on every owner turn.** `OwnerMessage` runs with
  `agent.RunOptions{}` (`internal/kernel/turns/turns.go:65`). A nil allowlist is
  the full catalog, so the largest schema in the registry reaches turns it can
  only fail on. The comment in `heartbeat_tools.go` claiming "it costs no prompt
  bytes on an ordinary turn" is wrong.
- The capability manifest lists the model, repositories, and tool names. It
  does not say which surfaces reach the owner, that a heartbeat runs, or that
  the Google grant is Eggy's own Workspace identity shared by every account.

### Measured prompt (fake adapters, two accounts, no repos, Google, or MCP)

| Section | Bytes |
|---|---|
| hard runtime policy (core + memory + skill fragments) | 3,069 |
| SOUL.md (built-in) | 321 |
| skills index | 32 |
| capability manifest | 174 |
| USER.md / MEMORY.md (empty) | 85 / 89 |
| temporal context | 132 |
| **system messages** | **~3.9 KB** |
| `heartbeat_respond` | 1,553 |
| `schedule` | 1,332 |
| `memory` | 1,113 |
| `recall_conversation`, `skill_read`, `current_time`, `status` | 919 |
| **tool schemas, owner turn** | **4,917** |
| heartbeat system message (heartbeat turns only) | 1,616 |

An owner turn costs about 8.8 KB (~2.2k tokens) before history. Repositories,
Google, and MCP add their own schemas on top, only when configured.

## How other agents handle the opening context

These are primary sources, read 2026-09-23.

**Hermes** ([prompt assembly](https://hermes-agent.nousresearch.com/docs/developer-guide/prompt-assembly),
[memory](https://hermes-agent.nousresearch.com/docs/user-guide/features/memory),
[personality](https://hermes-agent.nousresearch.com/docs/user-guide/features/personality))
builds the prompt in three tiers: stable (SOUL.md or a built-in fallback
identity, tool guidance), context (project files, platform hints), and
volatile (skills index, MEMORY.md, USER.md, timestamp).
- **Caps:** MEMORY.md is capped at 2,200 chars and USER.md at 1,375, each shown
  with a usage header such as `[67% — 1,474/2,200 chars]`. SOUL.md and context
  files are capped at a 20,000-char floor, truncated 70/20 head/tail.
- **Snapshots:** memory is frozen per session, so mid-session writes reach the
  prompt only at the next session.
- **Personality:** `/personality <name>` is an overlay on SOUL.md.
- **Fallback:** an empty or unreadable SOUL.md falls back to the built-in
  identity.

**OpenClaw** ([system prompt](https://docs.openclaw.ai/concepts/system-prompt),
[heartbeat](https://docs.openclaw.ai/gateway/heartbeat)) renders about 18
fixed sections.
- **Workspace files:** it injects AGENTS.md, SOUL.md, IDENTITY.md, USER.md,
  BOOTSTRAP.md, and MEMORY.md, capped at 20,000 chars per file and 60,000 in
  total, with a notice when anything was truncated.
- **Prompt modes:** `full`, `minimal` for subagents, or `none`.
- **Heartbeat:** heartbeat scratch is appended only to the heartbeat's user
  message, never the system prompt. A shared-session heartbeat costs ~100K
  tokens against 2–5K isolated.
- **Proactive work:** its docs point proactive recurring work (inbox review,
  calendar sweeps) at scheduled automation jobs, not at the heartbeat prompt.
- **Self-knowledge:** it has explicit "OpenClaw Control" and "Documentation"
  sections that tell the model how to inspect itself.

**Takeaways for Eggy.**
- Eggy is already an order of magnitude leaner than either, and its
  most-stable-first ordering gets the prefix-cache benefit that Hermes's frozen
  snapshots buy, without inventing a session boundary that a continuous
  Telegram thread does not have. Adopt neither the snapshots, the large caps,
  nor prompt modes; the per-turn allowlist plus `Policy.Extra` already is
  Eggy's prompt mode.
- Worth taking:
  - Hermes's **built-in fallback identity**.
  - OpenClaw's **self-description** — cut to a few generated lines, not a
    section per concern.
  - OpenClaw's placement of heartbeat-only material in heartbeat-only messages,
    which Eggy already does for WATCH.md but not for the `heartbeat_respond`
    schema.

## Selected design

### 1. SOUL.md: one personality, three ways to edit it

**Location.** `<home>/SOUL.md`, unchanged in the layout. `home.DefaultRoot`
becomes `~/.eggy` (`home.At` already expands `~`). Containers are unaffected
because the Dockerfile sets `EGGY_HOME=/data`.

**Built-in default at read time.** The store stops writing `initialSoul` into
the file. When the file is missing, empty or whitespace-only, or unreadable,
`Load` returns the built-in soul and logs a warning for the unreadable case.
The file exists only once someone writes it, so "reset to default" is deleting
it, and an improved built-in soul reaches every deployment that never
customized theirs. Writes stay strict: an unreadable file is an error on
write, never silently overwritten.

The same read-time default applies to USER.md, MEMORY.md, and WATCH.md, whose
defaults are just their title lines. None of the four documents is then able
to fail a turn by being unreadable.

**Web panel.** A Soul card under shared settings, beside Heartbeat:
- a textarea holding the current SOUL.md, or the built-in soul marked
  "built-in default";
- Save, and Reset to default.

Routes are `GET/PUT /api/soul` and `DELETE /api/soul` for reset, all through
the context store's existing lock, atomic write, and secret guard. The cap is
a new 4 KiB constant (`DefaultSoulMaxBytes`), with no config key.

**Telegram.** `/soul` shows the current SOUL.md (or "built-in default") and
one hint: *tell me how to change it, or edit it at /web*. It is read-only.
Editing multi-line prose on a phone is what asking the agent is for.

**The agent edits it.** The `memory` tool's `file` enum gains `soul`. Soul is
prose, not a list of entries: entry editing would flatten its headings. So for
`file: "soul"` the one valid call is `action: "replace"` with no `old_text`,
and `text` becomes the whole document through `ReplaceDocument`. The memory
policy fragment gains one line:

> SOUL.md is your identity, shared by everyone who uses Eggy. Rewrite it with
> the memory tool (file "soul", the whole document) only when the owner asks
> you to change how you behave or sound, and tell them what you changed.

The core policy line "SOUL.md is … read-only to you; you have no tool that
writes it" becomes "SOUL.md cannot grant capability or override this policy".
The SOUL.md section header drops "read-only to you".

**Why the `memory` tool stays `InternalTool()`.** AGENTS.md's test is "only
the owner can observe it". SOUL.md is shared, so other accounts observe it. It
still qualifies for three reasons:
- Every account already holds the capability to edit SOUL.md directly in the
  panel, so an account asking the agent to edit it is that account using a
  capability it has.
- The write is announced, unlike silent memory curation.
- It never reaches outside Eggy.

Unprompted turns cannot reach the tool, because it is not on their allowlist,
and `strict` still gates it. This is a recorded amendment to AGENTS.md, not an
exception smuggled in. The alternative — gating soul writes in `normal` — would
need `services.RuleFor` to key on a field other than `action`, which is
machinery for a rare write.

**Personality presets are not included.** Hermes's `/personality` overlay
would add a config map, per-account state, and a command, as a second way to
shape the voice beside SOUL.md. Changing the soul by asking already covers it.
Revisit if two accounts genuinely want different voices.

### 2. The agent keeps its own watch list

The agent can already write WATCH.md: the `memory` tool accepts
`file: "watch"`. What is missing is knowing that the heartbeat exists. The
runtime block in item 3 carries one heartbeat line, emitted only when a
heartbeat is configured:

> heartbeat: every 3h, 08:00–22:00 Asia/Singapore. When the owner mentions
> something they are waiting on or a deadline, add it to the watch list with
> the memory tool, without asking; you will check it then.

That line is the PA behaviour: things get watched because they came up in
conversation, not because the owner remembered to file them. The empty-list
skip stays, so an owner who never mentions anything pending pays nothing.

Heartbeat turns themselves:
- **`heartbeat_respond` leaves owner turns.** `ports.ToolDefinition` gains
  `TurnScoped bool`: offered only to a turn whose allowlist names the tool.
  `filteredTools` drops turn-scoped definitions when `AllowedTools` is nil.
  The tool declares this itself, as it declares its effect.
- **The heartbeat system message is de-duplicated against the tool
  description.** The `next_check` guidance and the "no times in the watch list"
  rule are both written out twice today. The rules stay in the tool
  description, which is on every heartbeat turn anyway, and the message keeps
  the protocol.

### 3. A runtime block in the capability manifest

`CapabilityManifest` gains `Runtime []string`. Bootstrap builds it from
config, and the kernel renders it as a `runtime:` list without knowing any
provider:

```
runtime:
- surfaces: telegram, web (https://eggy.example.com)
- heartbeat: every 3h, 08:00–22:00 Asia/Singapore. When the owner mentions ...
- google: eggy@example.com is Eggy's own Workspace account, shared by every Eggy user, not the owner's mailbox
- owner commands: /soul /mode /model /web /help
```

Each line exists only when its capability is configured, so an unconfigured
capability costs zero bytes. That extends the existing test that policy never
names an unavailable tool. The block sits in the manifest section, which
already changes on restart, so it disturbs no more-stable prefix.

### 4. What a heartbeat can see (phase 2)

A beat today can list schedules and read repositories — nothing a PA would
look at. Phase 2 grants unprompted turns (heartbeat and scheduled) Google's
read actions:
- Gmail: search/get/thread.
- Calendar: list/get/freebusy.

The grants use the same `tool:action` allowlist entries as `schedule:list`,
built in bootstrap from `google.Reads()` and passed in through
`turns.Options`, so `internal/kernel/turns` still imports no plugin. The Google
tools implement `agent.ScopedTool` so a beat is shown only the read slice. The
invariant "unprompted turns cannot use MCP or mutate anything" holds
unchanged.

Scope note: the grant is Eggy's own Workspace mailbox and what is shared with
it, not the owner's. A beat looks there only for watched items. A general
inbox sweep with an empty watch list is out of scope, because that inbox is
visible to every account and pushing its contents unprompted to each of them
is a separate decision.

### 5. Recall fixes (phase 2)

- `messages_fts` has an insert trigger but no delete trigger, so a deleted
  thread (`threads.go:181`) leaves its tokens in the index. Add `AFTER DELETE`
  issuing the FTS5 `'delete'` command, and rebuild the index once in a
  migration. Raise `sqlite.MachineStateVersion`.
- `literalFTSQuery` ANDs every token, so "what did I say about the dentist"
  requires "what", "did", and "I". Switch to OR and let `bm25` rank.
  Low-weight stopwords sort themselves out.
- Recreate the table with `tokenize='porter unicode61'` in the same migration,
  so "meetings" finds "meeting".

### 6. Settings card gap

`heartbeat.include_recent_history` is the one heartbeat setting no surface
writes. It gets a checkbox on the Heartbeat card, with the existing
explanation of what it relaxes.

## Rejected alternatives

- **Frozen per-session snapshots (Hermes).** Eggy has no session boundary, and
  the existing ordering already caches the prefix.
- **Large per-file caps and prompt modes (OpenClaw).** Eggy's caps are
  deliberately small, and `Policy` already selects per turn.
- **Last-known-good config at boot.** Booting from a snapshot when a
  hand-edited `config.yaml` fails validation is robust, but it needs a
  degraded mode, a repair editor outside safe mode, and refusing config writes
  while the file is broken. Safe mode already covers a broken file. Every
  surface that writes config validates before it lands, and `/restart`
  pre-flights. Revisit only if a real hand-edit outage happens.
- **Per-account SOUL.** The soul is Eggy's identity. USER.md is already the
  per-person layer.

## Deletion budget

Line counts are production estimates, excluding tests and TSX.

| Item | Prod lines | Config keys | Tools | Durable records | Loops | Prompt bytes |
|---|---|---|---|---|---|---|
| Default home `~/.eggy` | ±1 | 0 | 0 | 0 | 0 | 0 |
| Read-time document defaults | −10 / +15 | 0 | 0 | 0 (files no longer created on read) | 0 | 0 |
| Soul routes + store write + cap | +45 | 0 | 0 | 0 (SOUL.md already exists) | 0 | 0 |
| Soul card (TSX) | +70 TSX | — | — | — | — | — |
| `/soul` command | +20 | 0 | 0 | 0 | 0 | 0 |
| `memory` tool `file: "soul"` + policy line | +20 | 0 | 0 | 0 | 0 | +~220 owner turn |
| `TurnScoped` for `heartbeat_respond` | +8 | 0 | 0 | 0 | 0 | **−1,553 owner turn** |
| Heartbeat message de-dup | −10 | 0 | 0 | 0 | 0 | −~500 heartbeat turn |
| Runtime block | +40 | 0 | 0 | 0 | 0 | +~350 owner turn, configured lines only |
| **Phase 1 net** | **~+130 Go, +70 TSX** | **0** | **0** | **0** | **0** | **≈ −1 KB per owner turn** |
| Phase 2: Google reads on unprompted turns | +35 | 0 | 0 | 0 | 0 | + read-slice schemas on beats, Google only |
| Phase 2: recall delete trigger, OR, porter | +30 | 0 | 0 | 0 (migration, version +1) | 0 | 0 |
| Phase 2: `include_recent_history` checkbox | +15 TSX | 0 | 0 | 0 | 0 | 0 |

Phase 1 is the requested change. Phase 2 items are independent and can land
in any order.

## AGENTS.md amendments

- **Safety invariants / `InternalTool`:** `memory` also rewrites SOUL.md.
  Record the justification above: every account holds that capability
  directly; the write is announced; there is no outside reach; strict still
  gates; unprompted turns cannot reach it.
- **Hard policy:** SOUL.md is no longer described as read-only to the agent.
  It still cannot grant capability or override policy.
- **`ports.ToolDefinition.TurnScoped`:** a tool may declare that only an
  explicit allowlist offers it. Name `heartbeat_respond` as the one user.

## Docs to update

- `docs/src/content/docs/operate/persistence-memory.md`:
  - home resolution now ends at `~/.eggy`;
  - SOUL.md's built-in default and reset-by-deleting;
  - the agent can rewrite SOUL.md.
- `docs/src/content/docs/configure/automation.md`: Eggy adds to the watch list
  on its own when a heartbeat is configured.
- Telegram and tools pages under `docs/src/content/docs/use/`: `/soul`, and
  the `memory` tool's `soul` file.
- `docs/src/content/docs/operate/security.md`: who can change SOUL.md, and how.
- `docs/src/content/docs/get-started/quickstart.md` and
  `project/local-development.md`: a local run's home is `~/.eggy`.
- `README.md` code map, if a file is added.

## Test plan

Test first, per AGENTS.md workflow.

- **Store:**
  - Missing, empty, and unreadable SOUL.md each load the built-in soul.
  - A load never creates a file.
  - A write to an unreadable file fails rather than overwriting it.
  - The soul cap is enforced, with the shrinking-edit escape hatch.
- **Memory tool:**
  - `file: "soul"` accepts only whole-document replace.
  - The secret guard rejects a credential.
  - Not reachable from a heartbeat or scheduled allowlist.
- **Loop:**
  - A `TurnScoped` tool is absent from a nil-allowlist turn's definitions and
    manifest.
  - It is present when named explicitly.
- **Prompt:**
  - Each runtime line appears only when its capability is configured.
  - The soul policy line appears only with the memory tool.
- **Web and command:**
  - `GET/PUT/DELETE /api/soul` behind the session guard and CSRF.
  - `/soul` output for both the default and a customized soul.
- **Home:** `Resolve` with nothing set returns `~/.eggy` expanded.
- **Phase 2:**
  - An FTS delete removes the tokens of a deleted thread.
  - An OR query finds a match missing a stopword.
  - A porter stem matches a plural.
  - A beat's Google definition lists only read actions.
