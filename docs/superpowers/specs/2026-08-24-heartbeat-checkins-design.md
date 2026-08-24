# Heartbeat v2: a check-in that remembers what it already said

Date: 2026-08-24
Status: designed

Supersedes nothing. This extends the heartbeat shipped by
`2026-08-02-heartbeat-design.md`, which stays accurate for everything it
describes: the ticker, the isolation, the silence protocol, and the reasons the
heartbeat is neither a plugin nor a dispatcher event. Read that first.

## Problem

The v1 heartbeat can stay silent. It cannot check in.

Three gaps separate those, and only the first is obvious:

1. **It has nothing to check in about.** Its allowlist is `status`,
   `repository_list`, `read_file`, `repository_github`, `workspace_open`,
   `workspace_close`, `skill_read`, and `schedule:list`. There is no task
   concept anywhere in the tree — "task" today means a cron schedule, and a
   schedule already speaks for itself at fire time. So a beat has the
   repository, the clock, and nothing the owner has actually asked it to watch.

2. **It has no memory between beats.** Each beat is isolated by construction:
   no ambient conversation history, and no record of what the previous beat
   said. A finding that warrants speaking once therefore warrants speaking
   again at the next interval, and the one after that. `HEARTBEAT_OK` protects
   the owner from beats with nothing to say; nothing protects them from a beat
   with the *same* thing to say. This is the defect that makes the current
   heartbeat unusable at any interval short enough to be useful.

3. **It has no sense of timing.** A fixed interval fires at 03:00. v1 deferred
   this deliberately and named muting the Telegram chat as the interim answer,
   which is a workaround that disables the feature.

Gap 2 is the one that matters. Gaps 1 and 3 make the heartbeat useless; gap 2
makes it actively annoying, which is worse, because an annoying notification
gets muted and a muted heartbeat is a standing token cost that can never
produce a visible message.

## Prior art

Read on 2026-08-24 from primary sources: `docs.openclaw.ai/gateway/heartbeat`
and `docs.openclaw.ai/automation`. Secondhand summaries of both were checked
and found wrong on two points recorded below.

**NanoClaw has no heartbeat.** It is not a model for this. Search results
suggesting otherwise were describing OpenClaw.

**OpenClaw's answer to "what does a heartbeat look at" is a monitor scratch** —
and it is neither the memory document nor the scheduler:

- A small checklist (256 KiB cap) held in the database, appended to the
  heartbeat prompt each beat, rewritable by the agent through
  `heartbeat_respond(scratch: ...)`.
- `heartbeat_respond` also carries `notify`. With `notify: false` the agent
  records an internal state update that is remembered as bounded context for
  the next turn **without** messaging the owner. That is the anti-repetition
  mechanism, and it is a structured tool call rather than a parsed string.
- The stock prompt says, verbatim: *"Recurring tasks are automations; create or
  change their schedules with the automations tool, not heartbeat scratch. Do
  not infer or repeat old tasks from prior chats."*
- `activeHours` (`start` inclusive, `end` exclusive, IANA timezone) skips beats
  outside the window.
- A scratch that is effectively empty — only whitespace, comments, headings, or
  fence markers — skips the run entirely, `reason=empty-heartbeat-file`, with
  no model call.
- `HEARTBEAT_OK` in the *middle* of a reply is deliberately not special-cased.

### Why `HEARTBEAT.md` was deprecated, and why that is not an argument here

The v1 spec recorded that OpenClaw migrated workspace `HEARTBEAT.md` into a
database scratch, and read that as evidence against a file. Re-reading the
primary doc, the migration's substance is narrower: `openclaw doctor --fix`
*"converts old `tasks:` entries into independent automation jobs"* and archives
the file.

`HEARTBEAT.md` was not deprecated for being a file. It was deprecated for
growing a `tasks:` block with per-entry intervals — it had become a second
scheduler competing with automations, and the migration was principally about
dismantling that. The database move came along for the ride, carried by a
requirement Eggy does not have: OpenClaw serves many agents and needs private
per-agent scratch, while Eggy has one owner and one home.

`AGENTS.md` commits to Markdown as one of three deliberate durable forms —
"Markdown for owner-facing documents" — and a list of what Eggy is watching is
exactly that: something the owner should be able to open, read, and edit by
hand. So the watch list is a Markdown context document, and the real lesson
from the deprecation is carried as a constraint instead:

> **The watch document holds things to look at, never things with their own
> cadence.** An entry that wants a time is a schedule and belongs in the
> schedule store.

This is also the lesson Eggy has already learned once directly:
`internal/config/config_init.go:48` still carries `{"scheduler"}` in
`retiredConfigFields`, annotated "heartbeat and proactive messaging", and
`heartbeat_cadence` sits in the same retired list. A heartbeat that accretes
its own scheduling is the version that gets deleted.

## Design

### 1. The watch document — `memories/WATCH.md`

A fourth `ports.ContextDocument`, `ContextWatch`, agent-writable and
owner-editable.

It inherits, by construction rather than by re-decision, everything the two
existing agent-writable documents have: substring-addressed plain-line entries
(so nothing models its layout), `SecretGuard` validation on every write,
atomic writes through `plugins/atomicfile`, and a byte budget.

The budget is `DefaultWatchMaxBytes`, a package constant in
`plugins/context/markdown` beside `DefaultUserMaxBytes` and
`DefaultMemoryMaxBytes`. **Not a config key** — the existing two are not, and
a third would be three keys nobody sets.

**Owner curation costs zero new tools.** The existing `memory` tool's `file`
enum gains `"watch"` beside `"memory"` and `"user"`
(`writableDocument`, `internal/kernel/services/context.go:139`, and
`Store.writableDocument`, `plugins/context/markdown/store.go:168`). So "keep an
eye on the deploy" works conversationally, and the file is also just a file.

**It is not loaded on ordinary turns.** Rather than adding a field to the
rendered system prompt in `agent/prompt.go` — which would put the watch list in
front of every owner turn, and churn a document that R6 wants byte-stable for
prompt caching — `HeartbeatTurn` loads it and appends it as a `Policy.Extra`
system message. That is the mechanism `HeartbeatTurnMessage()` already uses,
documented at `turns.go:186` as being for "rules that govern one kind of turn
only". Owner turns pay nothing and `prompt.go`'s main path is untouched.

`ports.AgentContext` gains `Watch` and `WatchMaxBytes` for the web panel and
the capacity indicator, on the same pattern as `Memory`/`MemoryMaxBytes`.

### 2. `heartbeat_respond`

The v1 silence decision is string-sniffing: `silentReply` (`turns.go:263`)
strips a leading or trailing sentinel and drops the reply if under 300
characters of pleasantry. That heuristic exists because a text protocol cannot
carry a decision. Replace the decision with a tool.

`heartbeat_respond` is present **only** in the heartbeat allowlist, so it costs
zero prompt bytes on every other turn:

| field | meaning |
|---|---|
| `notify: true` + `notification_text` | deliver this to the owner |
| `notify: false` | stay silent, but record what was seen |
| `watch` (optional) | replace the watch list with this content |

The `watch` field replaces the whole document rather than editing entries.
That is a deliberate departure from how `USER.md` and `MEMORY.md` are written,
and it costs one port method: `ContextStore` today has only `AddEntry`,
`ReplaceEntry`, and `RemoveEntry`, so it gains
`ReplaceDocument(ctx, document, content) error`.

Whole-document replacement is the right shape here even at that price. The
document is budget-capped and small enough to rewrite in full; a beat that
wants to annotate three entries and drop a fourth would otherwise need four
correct substring matches in a row, and a single failed match leaves the watch
list half-updated with no transaction to roll back. Rewriting it whole is one
atomic write through the path `rewrite` already uses. `ReplaceDocument` is
subject to the same `writableDocument` gate as the entry methods, so `soul`
stays load-only.

`notify: false` is the fix for gap 2, and the silent update lands in
`WATCH.md` itself rather than in a new store: an entry becomes
`PR #18 open since Aug 20 — mentioned Aug 22`. The next beat reads that entry
and stays quiet. Durable across restarts, visible to the owner, and no new
durable record.

A `watch` payload is validated by `SecretGuard` and against the byte budget on
the same path as any other write to that document. A rejected write fails the
tool call, not the turn; the notification still delivers.

The sentinel stays as the fallback for a model that answers in prose rather
than calling the tool. `silentReply` is unchanged and still applies when no
`heartbeat_respond` call was made. A structured response takes precedence over
the text fallback.

### 3. Cadence

**Active hours.** `heartbeat.active_hours.start` and `.end`, `HH:MM`, start
inclusive and end exclusive. A tick outside the window is skipped before any
model call. The window uses the timezone `turns.Options` already carries
(`Location`/`Timezone`) — **no new timezone key**. An unset or partially set
`active_hours` means always active, so existing configs are unaffected. A
window whose `end` is before its `start` wraps midnight; `24:00` is accepted as
`end`.

This retires the v1 spec's "Deliberate omissions → Quiet hours" item.

**Empty watch list skips the beat, with no model call.** A watch document
containing only whitespace and Markdown headings means the owner has asked for
nothing to be watched, so there is nothing to check. Taken from OpenClaw's
`reason=empty-heartbeat-file`.

This is the aggressive reading and it is chosen deliberately: it makes the
heartbeat's behaviour predictable — it checks what is written down, and nothing
else — and predictability is most of what "natural" means for something that
messages you unprompted. It also keeps the standing rule that an unconfigured
capability is free, which `heartbeatTicks` already honours with its nil
channel: an owner who sets an interval but never writes a watch list pays for
no model calls at all.

The alternative — beat anyway and let the model roam over `status` and
`schedule:list` — is recorded under deliberate omissions.

**The existing `Active()` guard is unchanged.** Ticks cannot pile up, and a
beat never interrupts a live conversation.

### 4. Isolation

`Policy.IncludeRecentHistory` becomes `true` for heartbeat turns, gated on
`heartbeat.include_recent_history`, **defaulting to false**.

The default is off even though this deployment wants it on. `AGENTS.md` and
`turns.go:236` both record the no-ambient-history rule for unprompted turns as
a standing invariant, and the reason holds: an owner's earlier chat should not
silently steer a turn they are not present for and did not review at fire time.
Defaulting the key to false keeps that invariant intact for anyone who does not
opt in, makes the relaxation one auditable line in `config.yaml`, and gives
`/status` something to report.

The window is already bounded. `ConversationService.RecentMessages`
(`internal/kernel/services/conversation.go:37`) returns at most `recentLimit`
messages, default 20 — this is not OpenClaw's full-session context, which their
own documentation puts at roughly 100K tokens against 2–5K isolated. The cost
here is approximately twenty messages per beat.

**Tools stay read-only regardless.** The allowlist is `ReadOnlyTools()` plus
`heartbeat_respond`. History changes what a beat knows, never what it can do,
and no unprompted allowlist names an MCP tool or a mutation tool.

## Changes

| file | change |
|---|---|
| `internal/ports/ports.go` | `ContextWatch`; `AgentContext.Watch`, `.WatchMaxBytes`; `ContextStore.ReplaceDocument` |
| `plugins/context/markdown/store.go` | `DefaultWatchMaxBytes`, `Paths.Watch`, `initialWatch`, `writableDocument` case, `Load`, `ReplaceDocument` |
| `internal/kernel/services/context.go` | `writableDocument` accepts `"watch"`; tool schema enum |
| `internal/kernel/services/heartbeat_tools.go` | new: `heartbeat_respond` |
| `internal/kernel/agent/prompt.go` | `HeartbeatTurnMessage()` gains the tool protocol and the no-cadence rule |
| `internal/kernel/turns/turns.go` | `HeartbeatTurn` loads the watch doc, appends it as `Extra`, allows the tool, honours the history key; `run` prefers a structured response |
| `internal/bootstrap/app_events.go` | active-hours and empty-watch-list guards in `onHeartbeatTick` |
| `internal/config/config.go` | `ActiveHours`, `IncludeRecentHistory`; validation |
| `internal/config/config_mutate.go` | `SetHeartbeat` carries the new fields |
| `internal/home/home.go` | `memories/WATCH.md` path |

No new packages, port *interfaces*, event types, durable forms, durable
records, or goroutines. One method is added to an existing port
(`ContextStore.ReplaceDocument`), which every in-tree fake implementing that
interface must gain.

## Error handling

- A failing beat is logged and not retried; the next tick is the retry. This is
  v1 behaviour and is unchanged — a heartbeat has no durable claim to release.
- A `watch` payload rejected by `SecretGuard` or the byte budget fails that
  tool call with the store's error. The model may retry with less content, and
  the notification still delivers. A failed watch write never fails the turn.
- A malformed `active_hours` value is rejected at config load, not silently
  ignored: a quiet-hours window that does not work is indistinguishable from a
  broken heartbeat.
- An empty configured instruction still falls back to the built-in default.
- The v1 startup warning when `heartbeat.interval` is set with no Telegram
  channel configured is unchanged.

## Testing

Test-first, extending `internal/bootstrap/heartbeat_test.go`,
`internal/kernel/turns/turns_test.go`, and
`plugins/context/markdown/store_test.go`.

Cadence:
- A tick outside `active_hours` runs no turn.
- A tick inside the window runs one.
- A window wrapping midnight admits the hours on both sides of it.
- An unset `active_hours` is always active.
- An empty or headings-only watch list makes no model call.
- A beat is skipped while a turn is already active.

Repetition:
- `notify: false` delivers nothing and writes the watch list.
- `notify: true` delivers `notification_text`.
- A finding already annotated in the watch list produces no second delivery.
- A prose reply with no tool call still falls back to `silentReply`.
- A structured response wins over a conflicting text reply.

Isolation:
- A heartbeat carries no recent history when the key is unset.
- It carries a bounded window when the key is set.
- Its allowlist names no MCP tool and no mutation tool, with or without the
  key — the existing `TestNoUnpromptedAllowlistNamesAnMCPTool` and
  `TestUnpromptedTurnsRunWithARestrictedAllowlist` assertions extended.

Document:
- A `watch` payload containing an active secret is rejected.
- A write past `DefaultWatchMaxBytes` is rejected with the consolidate message.
- `ReplaceDocument` against `soul` is rejected as read-only.
- A rejected `watch` payload leaves the document unchanged and still delivers
  the notification.
- `WATCH.md` is absent from an ordinary owner turn's rendered prompt.
- A config with no `heartbeat:` section still loads under `KnownFields(true)`.

## Deletion budget

| | |
|---|---|
| production lines | ~+180 |
| config keys | +3 (`active_hours.start`, `active_hours.end`, `include_recent_history`) |
| tools | +1, heartbeat-only; +1 enum value on the existing `memory` tool |
| durable forms | 0 — Markdown, an existing one |
| durable records | 0 |
| background loops | 0 — the same `case` in the same select |
| new port interfaces | 0 |
| port methods | +1 (`ContextStore.ReplaceDocument`) |
| new files | 1 (`heartbeat_tools.go`) |

The honest accounting: this is net-additive, and the argument for it is that
the thing it prevents cannot be fixed by the code already present. A v1
heartbeat that finds something says it again every interval forever, and no
amount of prompt tuning fixes that, because the beat has no way to know it
already spoke. `notify: false` plus a durable watch list is the smallest
mechanism that gives it one.

Two of the three config keys buy back a v1 deferral (quiet hours) that was
costing the feature its usability, and the empty-watch-list skip means a
deployment that does not use this pays for zero model calls rather than one
every interval.

## Deliberate omissions

**Beating with an empty watch list.** Rejected above: it makes the heartbeat's
behaviour unpredictable, and an unprompted message the owner cannot predict is
the thing that gets muted. Roughly a one-line change if the predictability
argument turns out to be wrong.

**A structured task record.** Considered and rejected. It would be a fourth
durable form against `AGENTS.md`'s three, and `heartbeat_respond` plus
substring-addressed prose covers what a heartbeat needs: something to look at,
and somewhere to note that it looked. Due dates and status transitions are the
scheduler's job, and routing them there is the constraint that keeps the watch
list from becoming a second scheduler.

**Rate limiting the heartbeat.** Still rejected, for v1's reason: silence is
already the default and an explicit interval plus active hours makes the
cadence predictable. A cap would reintroduce unpredictable silence.

**Per-entry snooze.** A snooze is a cadence, and cadences are schedules. The
model annotating an entry with what it already said achieves the same thing
without giving the watch list its own timing semantics.

**Event-driven triggers.** Unchanged from v1: out of scope, needs an event
source port, a subscription store, and per-source authentication.

## Deploy note

`internal/config` parses with `KnownFields(true)`. This change only adds
optional keys to an optional section, so existing `/data/config.yaml` files
stay loadable and no migration is required. `memories/WATCH.md` is created on
first write, like the other context documents; an absent file reads as empty
and skips the beat, which is the correct behaviour for a deployment that has
not adopted this yet.
