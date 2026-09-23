---
title: Schedules and heartbeat
description: Run turns you are not present for — exact and recurring schedules, and a self-paced check-in driven by a watch list.
eyebrow: Configure
---

Eggy has two ways to act without you asking: **schedules**, which fire at a time
you named, and the **heartbeat**, which looks at a standing list of things to
watch and speaks only when there is something to say.

Both run as *unprompted* turns. They carry no ambient conversation history, use a
read-only tool allowlist, cannot reach MCP, and cannot mutate anything —
instruction text does not grant authority it was not given.

## Schedules

Schedules are machine-managed records in `eggy.db`, not configuration. They are
created in conversation and reviewed anywhere.

One `schedule` tool covers the whole subject:

- **`action=list`** reports every schedule with its cron expression, next run,
  and kind.
- **`action=create`** takes an instruction plus either `cron` (a five-field
  expression, recurring) or `at` (one-time, RFC3339).
- **`action=cancel`** removes one by id so it never runs again.

So a schedule created in conversation can be reviewed and taken back there too.
The panel's **Settings → Automation** page lists the same schedules and cancels
them; creating one stays conversational, because an instruction is prose.

### Two kinds

`kind=agent` — the default — starts a self-contained read-only agent turn with
the instruction as its request.

`kind=reminder` delivers the instruction verbatim at fire time **with no model
call at all**. When you want exactly these words at exactly this time, a
deterministic message is both cheaper and more reliable than asking a model to
repeat something.

Cron expressions and one-time times are read in `agent.timezone`.

## Heartbeat

Omitted, the heartbeat costs nothing: no ticker, no goroutine, no model call. Set
an interval to give the deployment a cadence:

```yaml
heartbeat:
  interval: 3h
  # instruction: "Check the deploy and open pull requests."
  # active_hours:
  #   start: "08:00"
  #   end: "22:00"
  # include_recent_history: false
```

`3h` is a reasonable starting interval. Each beat runs an isolated read-only turn
and delivers to Telegram only when there is something worth saying — a heartbeat
is not one message per tick. A beat is skipped while another turn is running.
Without a `telegram` block there is nowhere to deliver unprompted output, so the
heartbeat stays off and says so once at startup.

### Your switch

The interval is shared; whether Eggy checks in on *you* is yours. Each person's
heartbeat is **off until they turn it on**, with `/heartbeat on` on Telegram or
the **Heartbeat check-ins** switch under **Settings → Model & approvals**.
`/heartbeat off` stops it, and bare `/heartbeat` reports where it stands. The
switch follows unprompted delivery, which is Telegram today; a future channel
that can deliver unprompted messages uses the same switch.

Turning it on also tells Eggy, in every turn, that check-ins happen and when.
So when you mention something you are waiting on — a delivery, a reply, a
deadline — Eggy adds it to your watch list on its own, and the next beat looks
at it. You do not have to remember to file things.

### The watch list

A beat checks `memories/WATCH.md`, the standing list of what you have asked Eggy
to keep an eye on. It annotates that list with what it has already reported and
reads those notes on the next beat — which is how it tells "already mentioned
this" from "new", and what stops a finding worth reporting once from being
reported every interval.

A watch entry is a thing to look at, never a thing with its own cadence. Anything
that should happen at a particular time is a schedule.

**An empty watch list skips the beat entirely, with no model call**, and warns
once so the silence is distinguishable from a bug. An interval alone therefore
does nothing until someone has switched their heartbeat on and something is on
their list to watch.

Three surfaces write the list: **Settings → Automation** has a watch-list editor,
Eggy's own `memory` tool writes to it when you ask it to keep an eye on
something — or, with your heartbeat on, when you mention something pending —
and a beat annotates it. The panel seeds the editor on load and writes
back only on Save, so reopening the page shows whatever the last beat left.

### Self-pacing

`interval` is the starting point, not a fixed metronome. Every beat ends by
saying when it wants to look again — aimed at the next moment something it
watches could change, not at how long it can bear to wait. If a reminder is due
at 15:00 and needs an hour of preparation, the beat comes back at 14:00; if
nothing moves before Monday, it sleeps until Monday.

That request is clamped rather than rejected: never sooner than **five minutes**,
and never later than **eight times the configured interval**. The model's
judgement about direction is worth keeping even when its magnitude is off, and
rejecting it would cost a round trip to learn a bound it cannot see. Each beat
logs what it requested and when it will actually wake, because a self-paced
heartbeat is otherwise unobservable: a quiet beat writes nothing anywhere, and
nobody could tell good pacing from a stopped clock.

The gap is measured from the end of one beat to the start of the next, so a slow
check-in does not shorten the interval that follows it.

### Active hours

`active_hours` confines beats to a window of your day, read in `agent.timezone`
rather than the host's clock. `start` is inclusive, `end` is exclusive, and
`"24:00"` is accepted as an end so a window can run to midnight without the
wrapped `"00:00"` that would mean the opposite. A window whose `end` is before
its `start` wraps midnight, which is how an overnight watch is written. Both
bounds are required together, and a malformed window fails the config load rather
than silently suppressing every beat.

A beat that would fall inside quiet hours is moved to the window opening rather
than dropped, so the first beat of the day arrives at `start` instead of whenever
the interval happens to land after it.

### Conversation history

`include_recent_history` lets a beat see the recent conversation window, so it
can notice that you said you would ship something on Friday. It is **off by
default**: unprompted turns carry no ambient history, so your earlier chat cannot
silently steer a turn you are not present for and did not review when it fired.
Tools stay read-only either way — this changes what a beat knows, never what it
can do.

It is the one heartbeat setting no surface writes. Relaxing a safety invariant
should cost more than a tap on a phone, so it lives in `config.yaml` only.

### Editing from the panel

**Settings → Automation** edits the rest of this section. A blank interval there
means off, rather than leaving the previous interval in place; blank active hours
likewise clear the window. The instruction is preserved either way, so turning
the heartbeat back on does not mean retyping it. Like every other config section,
the panel writes `config.yaml` and the change applies on the next restart, which
`/restart` in chat performs — see
[Restarting to apply config](/eggy/configure/configuration/#restarting-to-apply-config).
