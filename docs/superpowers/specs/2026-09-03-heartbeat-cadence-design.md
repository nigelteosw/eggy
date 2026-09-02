# Heartbeat v3: a check-in that decides when to check in next

Date: 2026-09-03
Status: implemented 2026-09-03.

Two things changed between drafting and shipping, both recorded where they
apply:

- **The Problem section was rewritten** after reading the live watch list. It
  had framed this as a cadence-matching problem — fast things and slow things
  on one list. The deployment says it is a lead-time problem: the owner's one
  watch entry asks to be told *while there is still time to do it*. Same
  mechanism, smaller bet, and it made section 3's flat floor obviously right
  rather than a compromise.
- **The staging was dropped.** Three stages, a soak as a gate, and an
  owner-typed command held in reserve turned out to be scaffolding built to
  avoid trusting the model with the decision the feature exists to give it.
  See *Staging*.

Supersedes nothing. This closes the last open gap from
`2026-08-24-heartbeat-checkins-design.md`, which stays accurate for everything
it describes: the watch list, `heartbeat_respond`, the anti-repetition
mechanism, active hours, and the isolation rules. Read that first, and
`2026-08-02-heartbeat-design.md` before it.

## Problem

**Eggy delivers on a clock that knows nothing about when its information is
useful.**

Information has a window. "The deploy is failing" is worth hearing for minutes.
"The 15:00 thing needs something ready first" is worth hearing at 13:00 and
worthless at 14:58. A fixed interval fires on a phase unrelated to any of those
windows, so the beat that is too slow for one is too fast for the other.

The live watch list says it plainly. Read from the deployment on 2026-09-03, it
holds exactly one entry:

> Reminders due in the next few hours that need something ready first — say so
> while there is still time to do it

The operative words are *while there is still time to do it*. That is not a
request for a rate. It is a request for lead time.

v2 named this gap and closed half of it:

> 3. **It has no sense of timing.** A fixed interval fires at 03:00.

`active_hours` answered the 03:00 half. But a mute window is not timing. Inside
it the cadence is still one `time.NewTicker(interval)`, and nothing anywhere
reasons about how early the owner needs to hear something.

Two things follow, and they separate cleanly:

1. **A beat does not look ahead.** `schedule:list` is already in its allowlist
   (`turns.ReadOnlyTools`), so it can see that a reminder is due at 15:00 — but
   nothing tells it to look, so it does not.
2. **A beat cannot act on what it sees.** Even knowing about the 15:00
   reminder, it cannot arrange to be awake at 13:30. Its next wake is a
   constant.

The first is a sentence of prompt. The second is this spec.

Cadence-matching — a PR that moves weekly against a deploy that moves in
minutes — is a real consequence of the same defect, and it is why the bounds in
section 3 are wide. But it is the symptom, not the problem. Optimising for it
produces a heartbeat with a good average and bad timing.

### The morning defect

One smaller bug lives in the same code, and the same fix closes it. An
out-of-window tick returns early and the next attempt is a whole interval
later, so a window opening at 08:00 with a 3h interval may not beat until
10:00. The heartbeat is late by up to one interval every morning, which is
exactly when lead time is worth most.

## Prior art

Read on 2026-09-03 from the primary source, `docs.openclaw.ai/gateway/heartbeat`,
because this design departs from it and the departure should be argued against
what the reference actually says rather than against a memory of it.

**OpenClaw's heartbeat is fixed-interval and the agent cannot reschedule it.**
It is configured with `every: "30m"` and implemented as one system-owned
automation job per heartbeat-enabled agent — "a scheduled main-session turn" on
a predictable cadence. `heartbeat_respond` carries the monitor scratch and
`notify`, and no field resembling `next_check`, `interval`, or `backoff`. The
agent may rewrite its scratch, but only as context for the next beat, never to
move the next beat.

Their documented answer to this problem is explicit: *"If you need recurring
work with variable timing, create automations. Each job executes its configured
payload on its own schedule."*

So the reference does not merely omit this feature. It routes the need
somewhere else, and that is the same place Eggy's own v2 spec routes it:

> **The watch document holds things to look at, never things with their own
> cadence.** An entry that wants a time is a schedule and belongs in the
> schedule store.

That rule is load-bearing. `internal/config/config_init.go` still lists
`scheduler` and `heartbeat_cadence` in `retiredConfigFields`; a heartbeat that
grows its own scheduling is the version Eggy already deleted once. Any design
here has to survive the objection, not dodge it.

**Hermes Agent does not do it either**, read the same day from
`website/docs/user-guide/features/heartbeat.md` in `NousResearch/hermes-agent`.
Its heartbeat is one recurring instruction attached to a session —
`/heartbeat every <interval> <prompt>`, from `90s` to `1d`, minimum 60s —
firing as a normal user turn in the same conversation and the same prompt
cache. The documentation is explicit that the agent cannot change its own
interval; only the owner can, by command. Issue #15400, the nearest feature
request, specifies fixed schedules throughout and was closed as not planned
with no stated reasoning, so it is weak evidence in either direction.

So two independent references, designed by people who thought about this,
both decline agent-controlled pacing. That is the strongest argument against
this spec and it is recorded here rather than buried.

What they have instead is the observation that reframes the problem: **both
give the owner a conversational way to change cadence** — Hermes a command,
OpenClaw an automation — and Eggy has neither that nor self-pacing. The gap is
not really "the model cannot pace itself". It is that cadence is reachable only
from `config.yaml`.

That admits two answers, and this spec deliberately takes the harder one. An
owner-typed command is cheaper and rests on no unproven premise, and it is
rejected under deliberate omissions for a single reason: it only works when the
owner already knows what is coming and remembers to say so. A heartbeat exists
for the times they do not.

Two other things are worth taking from Hermes directly, and are:

- **The timer re-anchors on every fire**, so a slow beat shifts the phase
  rather than compressing the gap after it. Section 4.
- **Missed ticks coalesce** into one turn rather than a backlog. Eggy already
  has this for free — a Go ticker drops ticks the receiver is not ready for,
  and the `Active()` guard drops the rest — so their gateway backlog bug
  (#85119) is not a hazard here. Recorded so the next reader does not go
  looking for the mechanism.

Eggy is ahead of both references in two places, which is worth stating so this
spec is not read as catching up: Hermes has no quiet hours, and its
"don't-invent-work guard" is a prompt nudge where v2 has a structured
`notify: false` plus a durable watch annotation.

### Why `next_check` is not a second scheduler

The rule forbids a *cadence per watched thing*. That is what `HEARTBEAT.md`'s
`tasks:` block was, what OpenClaw dismantled, and what would put a scheduler
inside a Markdown document with no cancellation, no listing, and no
persistence.

`next_check` is not that. Precisely:

- **It carries no instruction.** A schedule is `(when, what)`. `next_check` is
  a bare duration; the *what* is always the same single beat, running the same
  configured instruction over the same watch list. It cannot express "do X at
  09:00", which is the only thing a schedule is for.
- **It is one number for the whole beat, never one per item.** There is no
  place to write a per-item cadence, because the field is on the tool call and
  not in the document. The existing prompt rule — never put a time, interval,
  or cron expression in the watch list — is unchanged and still enforced by
  `HeartbeatTurnMessage`.
- **It is relative, not absolute.** "In 45 minutes", never "at 09:00". An
  absolute time is a schedule wearing a different hat, so the schema does not
  admit one.
- **It is not durable.** It survives until the process restarts and no longer.
  A schedule that vanishes on restart would be a broken schedule; a pacing hint
  that vanishes on restart is merely a beat that returns to its anchor cadence.
  This is the sharpest line between the two, and it is worth paying a small
  cost to keep (see *Restart*).
- **It is bounded.** `next_check` moves only within a floor and a ceiling the
  owner did not have to think about but cannot be talked past, so no beat can
  become a hot loop or talk itself into a week of silence.

Under those five constraints the feature is "the beat may move its own next
wake, within bounds it cannot widen". That is a property of the clock,
not a new store, and it is why this is the last piece of gap 3 rather than a
reopening of the retired `heartbeat_cadence`.

## Design

### 1. `heartbeat_respond` gains a required `next_check`

One field on the tool that already exists, and it is **required**, beside
`notify`:

| field | meaning |
|---|---|
| `notify` + `notification_text` | unchanged |
| `watch` | unchanged |
| `next_check` (required) | a Go duration — when the next check-in should happen |

Required, not optional, for the reason `notify` is required. v2 replaced the
`HEARTBEAT_OK` string sniff with a structured decision because a decision left
implicit gets made wrong by default; pacing is the same decision one step
further out. An optional field builds the whole mechanism and then leaves the
fixed interval as what actually happens, because a model handed an out takes
it — and every beat that takes it is a beat that had just read the watch list
and looked at the world.

There is no state in which a beat that called this tool has no opinion. It
knows what it is watching and what it just saw; "when should I look again" is
strictly easier than the judgement it already made about whether to wake the
owner.

The configured interval survives, but only as a fallback for beats that never
made a decision at all — see section 2.

The value is a duration string (`"7m"`, `"45m"`, `"8h"`), parsed with
`time.ParseDuration`. A missing, unparseable, or non-positive value is a
tool-call error the model can retry, on the same footing as a
`notification_text` missing when `notify` is true — and, like a rejected
`watch` payload, it never fails the turn: the notification still delivers and
the beat falls back to the anchor interval. The finding is what the owner needs.

`HeartbeatResponse` gains `NextCheck time.Duration`, carried on the turn
context by the mechanism already there (`WithHeartbeatResponse`).

**The question the prompt asks decides what this field is worth.** Asked "how
long can you wait?", a model produces decay: a number that grows because
nothing happened, which is a politeness heuristic and not anticipation. Asked
**"when is the next moment this could change, or the owner could need this?"**,
it produces an aim: the deploy lands in about six minutes, so seven; standup is
at 09:30, so be there at 09:20; the pull request will not move before Monday,
so Monday. Same field, same clamp, entirely different behaviour, and the second
one is the point of the feature. `HeartbeatTurnMessage` asks the second
question.

### 2. What the configured interval now means

`heartbeat.interval` stops being the cadence and becomes the anchor. It is used
in exactly three places:

1. The first beat after start or restart, when no beat has yet decided
   anything.
2. The fallback for a beat that made no decision: a prose reply with no tool
   call, a failed beat, or a beat skipped before the model ran.
3. The derivation of the clamp band in section 3.

Nothing else reads it. An owner who sets `3h` is no longer saying "beat every
three hours"; they are saying "this is the rhythm to start from and fall back
to". `config.example.yaml` and the panel say so in those words, because an
owner who believes the old meaning will read a run of twelve-minute beats as a
bug.

The name stays. Renaming it would cost a `retiredConfigFields` entry and a
migration to buy a word, and the section it sits in is already named
`heartbeat`.

### 3. Bounds

`next_check` is clamped to a flat floor of five minutes and a ceiling of
`interval * 8` — with `interval: 3h`, anything from 5m to 24h.

The ceiling is relative because its job is to prevent silent death: a beat that
talks itself into a week of quiet has effectively turned the feature off, and
what counts as "too quiet" is exactly what the owner expressed by setting an
interval.

**The floor is flat, and deliberately not relative.** An earlier draft had
`interval/6`, which is wrong for the thing this feature is for: with
`interval: 3h` it forbids anything tighter than 30m, so a beat that knows the
deploy lands in four minutes structurally cannot be there for it. Being there
at the moment is the whole point; a floor that scales with the owner's idle
rhythm forbids exactly the case that matters most.

Five minutes is a token guard, not a pacing opinion — it stops a
misjudged beat from becoming a hot loop, and nothing else.

The residual risk is a model that sits near the floor for hours because
everything feels urgent. That is a prompt failure rather than a clamp failure,
and widening the floor would not fix it — it would only make the failure
slower and delete the deploy case at the same time. If the soak shows it, the
answer is the question in section 1 or a per-day beat budget, both cheaper than
losing the tight end of the range.

Both bounds are package constants in `internal/bootstrap`, beside the code that
applies them. **Not config keys**, for the reason `DefaultWatchMaxBytes` is not
one: the existing byte budgets are constants, and a `min_interval` /
`max_interval` pair would be two more keys nobody sets, on a section that
already has five.

Recorded under deliberate omissions: making these two keys, and why that is the
first thing to change if the band turns out wrong.

### 4. The ticker becomes a timer, and the timer respects the window

`heartbeatTicks` returns a ticker channel today. It becomes a resettable timer,
with the same nil-channel property for an unconfigured heartbeat — an
unconfigured heartbeat still costs nothing at runtime.

After each beat the next wake is computed in one place:

1. Start from the beat's clamped `next_check`, or the anchor interval when the
   beat made no decision at all (section 2).
2. If `now + that` falls outside `active_hours`, wake when the window next
   opens instead.
3. Reset the timer.

Step 2 is the morning-lateness fix, and it belongs here rather than in a
separate change because it is the same arithmetic: with a timer there is a
"when should the next beat be" expression to put it in, and with a ticker there
is not. It needs one pure method on the existing type —
`ActiveHours.NextOpen(when time.Time) (time.Duration, bool)` — which is
testable on its own and reuses `parseClock`.

**The timer re-anchors, and that is a behaviour change worth naming.** A
`time.Ticker` fires on a fixed phase, so a beat that runs four minutes is
followed by one only `interval - 4m` later. A timer reset after the beat
finishes puts a full gap after every beat, which is Hermes' documented
behaviour and the better of the two: the gap the owner configured is the gap
between beats *ending* and the next one starting, not a phase the beat's own
duration eats into. It also means a slow model cannot compress the interval
toward the floor.

Coalescing needs no code. A Go ticker already drops ticks the receiver is not
ready for, and `Active()` drops the rest; a timer that is only ever reset after
a beat completes cannot produce a backlog at all.

The existing `withinActiveHours` guard in `onHeartbeatTick` stays. A timer can
fire outside the window if the host clock jumps or the owner edits the window
mid-flight, and the guard is two lines that make the invariant true regardless
of how the wake was computed.

The `Active()` guard, the empty-watch-list skip and its once-only warning, and
the failing-beat-is-not-retried rule are all unchanged. A beat skipped for any
of those reasons reschedules at the anchor interval, because a skipped beat made
no decision and has no pacing to contribute.

### 5. Handing the decision back to the loop

`turns.HeartbeatTurn` builds the response context and discards the response
(`turns.go:263`). It gains a return value: `(services.HeartbeatResponse, error)`.

The beat runs in a worker goroutine while the daemon's select loop owns the
timer, so the duration crosses goroutines through a buffered channel — a
`chan time.Duration` of capacity one on `App`, with a case in the existing
select. A non-blocking send, so a beat that finishes while the loop is busy
cannot wedge a worker; a dropped hint costs one interval of imprecision and
nothing else.

This is +1 field, +1 select case, and no new goroutine. The alternative —
running the beat synchronously in the loop so it can reset the timer directly —
is rejected: it would block event handling for the length of a model call.

### 6. Close-out is prompt work, not a field

A watch item that opens and never audibly closes trains the owner to ignore the
channel: they hear "the deploy is failing" and never hear that it recovered.

This needs no schema change. `notify: true` already says "tell the owner"; what
is missing is the instruction that a watched thing *finishing* is one of the
things worth saying, and that the item then leaves the watch list. Both are
sentences in `HeartbeatTurnMessage`, which is where the v2 spec already said
behavioural fixes belong.

Deliberately no `resolved` field, no status enum, no state machine. The watch
list is prose the owner can edit, and status transitions are exactly what the
v2 spec refused a structured task record for.

### 7. The panel stops under-describing the heartbeat

With variable pacing, a panel that presents `3h` as the gap between checks is
lying. As shipped, the card says what the number now means — the rhythm the
heartbeat starts from, not the gap between every check — and
`config.example.yaml` says the same beside the key.

The row still prints the configured value and no band. Rendering the bounds
would be a pure function of config and cheap, but the number that would
actually help is the live one: when the next beat is, and what the last one
asked for. That is runtime state, and pushing it through the config surface to
get it onto a config card is the wrong shape. It goes with the beat-outcome
history under deliberate omissions; the pacing log is the interim answer.

### Restart

A restart returns the heartbeat to the anchor interval. The pacing hint is
in-memory only.

This is a real cost and it is chosen: persisting the next beat would mean a new
durable record for a value that is a hint, on a deployment where `TODO.md` has
state consolidation explicitly parked. It also keeps the sharpest line between
`next_check` and a schedule — a schedule is durable and this is not — which is
the argument section *Prior art* rests on.

The observable effect is bounded: after a restart the first beat comes at the
anchor interval, and one beat later the pacing is back.

## Staging

Shipped in one change on 2026-09-03, not staged.

An earlier draft split this three ways — clock, then lead-time prompt work,
then self-pacing behind v2's owed soak as a gate — with an owner-typed cadence
command held in reserve if the soak went badly. That structure was scaffolding
around a small change, and most of it existed to avoid trusting the model with
a decision the whole feature is about handing to the model. It is recorded
under deliberate omissions rather than kept.

What survived is the part a beat cannot work without: somewhere to put the
decision, a clock that honours it, and a prompt that says what the decision is
for.

The soak is still worth running, as observation rather than a gate — see
*Observability* — and the bounds in section 3 are what make running it safe
rather than the thing that has to pass first.

### Observability

Two things were true when this shipped, both found by reading the live
deployment on 2026-09-03, and neither is a reason to wait:

- **The watch list has no subject.** Its one entry is a standing instruction,
  not a thing with state, so there is nothing for a beat to annotate and the
  v2 anti-repetition mechanism has never been exercised. Fixing this is the
  owner's: put something with state on the list.
- **A beat left almost no trace.** A successful beat logged nothing at all, so
  there was no count, no notify rate, and no skip reason — two days of
  deployment logs held nine lines and one heartbeat entry, a provider outage.

The second is why `nextHeartbeatWake` now logs one line per beat carrying what
was requested and what was granted. That is the whole instrument: it needs no
storage decision, and it turns `railway logs` into a way to see whether the
pacing is any good. Read it before changing the bounds.

## Changes

As shipped:

| file | change |
|---|---|
| `internal/kernel/services/heartbeat_tools.go` | `next_check` required in the schema and validated in `Execute`; `HeartbeatResponse.NextCheck` |
| `internal/kernel/turns/turns.go` | `HeartbeatTurn` returns the beat's response |
| `internal/kernel/agent/prompt.go` | `HeartbeatTurnMessage` gains the look-ahead rule, the pacing rule, and the close-out rule |
| `internal/bootstrap/app_events.go` | `heartbeatClock` replaces the ticker; `ownerNow`; `nextHeartbeatWake`; `clampHeartbeatWake`; `finishHeartbeat`; `onHeartbeatTick` reports whether it dispatched; one log line per beat |
| `internal/bootstrap/app.go` | `heartbeatWake chan time.Duration` |
| `internal/config/config.go` | `ActiveHours.NextOpen` |
| `config.example.yaml`, `website/src/HeartbeatCard.tsx` | the interval is the rhythm to start from, not the gap between checks |
| `docs/src/content/docs/configure/configuration.md` | window snapping and the re-anchored gap |

No new packages, ports, port methods, tools, config keys, durable forms,
durable records, or goroutines.

## Error handling

- A missing, unparseable, or non-positive `next_check` fails that tool call
  with a message naming the accepted form. The notification still delivers and
  the beat falls back to the anchor interval. A required field is enforced in
  `Execute` rather than by the schema alone, on the same reasoning
  `scheduleSchema` gives for enforcing its required-field rules there: not
  every provider is relied on to honour the schema.
- A `next_check` outside the band is clamped silently rather than rejected: the
  model's judgement about *direction* is worth keeping even when its magnitude
  is wrong, and a rejection would cost a round trip to learn a bound it cannot
  see.
- A beat that never calls `heartbeat_respond` — the prose fallback — paces at
  the anchor interval.
- A failing beat is logged and not retried, and reschedules at the anchor
  interval. Unchanged from v1.
- A dropped pacing hint (loop busy) is not an error and is not logged as one.
- `interval: 0` still means off, with no timer and no goroutine.

## Testing

Test-first, extending `internal/bootstrap/heartbeat_test.go`,
`internal/kernel/turns/turns_test.go`, and
`internal/kernel/services/heartbeat_tools_test.go`.

The clock:
- A beat that takes measurable time is followed by a full interval, not by the
  remainder of a fixed phase. This is the re-anchoring assertion.
- No backlog of beats accumulates after a long-running beat.

Pacing:
- A beat returning `next_check` schedules the next wake at that duration.
- A value below the floor is clamped up; one above the ceiling is clamped down.
- The five-minute floor holds regardless of the configured interval, and a
  `next_check` well under the old `interval/6` — 7m against a 3h anchor —
  survives to the timer. This is the deploy case, and it is the assertion that
  fails if the relative floor ever comes back.
- An omitted `next_check` fails the tool call, still delivers the notification,
  and paces at the anchor interval.
- An unparseable or non-positive `next_check` does the same.
- A beat that never calls the tool paces at the anchor interval.
- A skipped beat (active turn, empty watch list, outside hours) paces at the
  anchor interval.

Window:
- A next wake landing outside `active_hours` is moved to the window opening.
- One landing inside it is not moved.
- `NextOpen` on a window wrapping midnight returns the near side.
- `NextOpen` on an unconfigured window reports no constraint.
- A timer firing outside the window still runs no turn.

Unchanged invariants, re-asserted:
- The heartbeat allowlist names no MCP tool and no mutation tool.
- `interval: 0` starts no timer.
- A config with no `heartbeat:` section loads under `KnownFields(true)`.
- `WATCH.md` is absent from an ordinary owner turn's rendered prompt.

## Deletion budget

| | |
|---|---|
| production lines | ~+120 |
| config keys | 0 |
| tools | 0 — one required field on an existing heartbeat-only tool |
| durable forms | 0 |
| durable records | 0 |
| background loops | 0 — the same select, one more case |
| new ports / port methods | 0 |
| new files | 0 |

The argument for the addition: a clock that fires on a phase unrelated to when
its information is useful is wrong in both directions at once, and no value of
`interval` fixes both. Too slow and the thing is reported after the owner could
have acted; too fast and the month-old pull request is re-read forty times a
day at full model cost. The beat is the only party in the system that knows
which case it is in — it has just read the watch list, and it can see what is
scheduled — and `next_check` is the smallest thing that lets it say so: no
store, no schema, no config key, one field and a timer where there was a
ticker.

Part of the cost is paid back in defects closed. The heartbeat stops being up
to one interval late every morning, a slow beat stops compressing the gap that
follows it, and a finding that resolves is now said out loud instead of just
going quiet.

## Deliberate omissions

**An owner-typed cadence command.** Both references' answer, and the cheapest
thing that closes the "cadence is only reachable from `config.yaml`" gap:
`/heartbeat 15m` from the phone, or a tool that lets the model set the interval
when the owner says to watch something closely. `SetHeartbeat` already exists
and already validates, so it is nearly free.

Rejected because it answers a different question. It works only when the owner
already knows what is coming and remembers to say so — and a heartbeat exists
for the times they do not. A cadence the owner has to set is a cadence that is
wrong every time they are busy, asleep, or unaware, which is most of the time
the heartbeat is running.

It also leaves a lever behind that has to be put back. An owner who sets 90s
for a deploy and forgets pays for it until they notice, and "until they notice"
on a channel designed to stay silent can be days.

If the pacing log shows the model cannot judge this, it is the fallback to
build — not a wider clamp band.

**Staging this, and gating it on a soak.** An earlier draft shipped the clock
first, then a lead-time prompt sentence, then `next_check` only once v2's owed
soak had shown the model annotates the watch list well.

Rejected once it was written down plainly. Every part of the structure existed
to postpone trusting the model with the one decision the feature is about
handing to it, and the gate would not have worked anyway: the live watch list
has no subject to annotate, so the soak had nothing to say. The bounds in
section 3 already cap what a misjudged beat can cost, which is what makes
shipping it directly reasonable — and the pacing log makes the judgement
visible afterwards, which is what the gate was really for.

Kept from that draft: the lead-time sentence, which is in
`HeartbeatTurnMessage` and cost nothing to include.

**An optional `next_check`.** The first draft of this spec had one, with
"absent means use the configured interval". Rejected in section 1: it makes the
fixed interval the path of least resistance for the model, which is the exact
behaviour the feature exists to remove, and it buys nothing — the anchor
interval is still there for beats that genuinely made no decision.

**A relative floor (`interval/6`).** Also in the first draft, also rejected, in
section 3. It forbids the tight end of the range, which is where anticipation
lives.

**`min_interval` / `max_interval` config keys.** Constants instead, per
section 3. If the band is wrong in practice the fix is the two constants; if it
is wrong *per deployment*, that is when it becomes two keys, and not before.

**Per-item pacing.** Rejected in *Prior art*, and it is the thing that would
turn the watch list into a scheduler. The watch list stays prose.

**Absolute times in `next_check`.** A time is a schedule. The `schedule` tool
exists and the prompt already routes there.

**Durable pacing across restart.** Rejected in *Restart*: a new durable record
for a hint, against a parked consolidation item.

**A `resolved` field on `heartbeat_respond`.** Section 5: prompt work covers
it, and a status enum is the structured task record v2 refused.

**Event-triggered beats.** The logical endpoint of aiming at moments is not
aiming at all: something happens — a webhook, a CI transition, a message — and
the beat runs because of it rather than because a timer expired. That is
strictly better than any pacing, and it is out of scope here by a wide margin:
it needs an inbound event source per watched thing, which is a new port, new
adapters, and a new trust boundary. `next_check` is what makes the clock useful
in the meantime, and it stays useful afterwards for everything that has no
event to subscribe to. Do not treat this spec as a step toward it; they are
independent.

**Beat outcome history in the panel.** The real follow-up. There is no record
of beats — no count, no notify rate, no skip reason — so nothing shows whether
the pacing is any good, and v2's soak is still owed. That matters more once the
interval varies, but it is a separate change with its own storage question, and
building it before there is variable pacing to observe would be measuring a
constant. Do it next, not here.
