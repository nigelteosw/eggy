---
title: Reading traces
description: Inspect a turn as it actually ran — every model call with its exact prompt, every tool call with its arguments and output.
eyebrow: Use Eggy
---

The transcript shows what Eggy *said*. A trace shows what it *did* to get there:
each model call with the exact prompt that produced it, each tool call with its
arguments and its result, in order, with timings and token counts.

Open **Traces** in the top navigation of the web panel, or go to `/traces`
directly. Tracing is on unless you switch it off — see
[Tracing](/eggy/configure/configuration/#tracing) for the settings and their
ceilings.

## Conversations, then turns

The list is grouped by conversation rather than by turn. A conversation collects
every turn on one thread, with its span of time, total duration, token total,
step count, and how many turns ended in an error.

`/clear` starts a new group. A cleared conversation keeps its identity but
begins a new session, and traces group on the two together — so the group you
are reading is the stretch of work that shared a context window, which is the
unit you actually want to compare turns within.

Pick a conversation, then a turn inside it.

## Inside one turn

A turn opens as a waterfall: one bar per step, positioned and sized by when it
started and how long it took, so a turn that spent eleven seconds in one tool
call reads as that at a glance rather than as a list of equal-looking rows.

There are exactly two kinds of step:

- **`model_call`** — one request to the provider and the response. Its request
  body is the full prompt: system messages, durable context, the live message
  window, and the tool schemas that were sent on that call. Its token counts are
  the provider's own, including cached prompt tokens where the provider reports
  them.
- **`tool_call`** — one tool execution: the arguments the model chose and what
  came back, or the error if it failed.

Selecting a step opens it beside the timeline without moving it. Bodies are shown
as the text that actually crossed the boundary — anything that parses as JSON is
pretty-printed, anything else is shown exactly as recorded.

A turn also carries where it came from (web or Telegram, direct or scheduled or
heartbeat), the model and reasoning effort it ran on, and whether it completed.
A turn still running shows as incomplete rather than being hidden.

## Why the prompt is worth reading

Most surprising answers are not model failures — they are context failures. The
prompt is where you see that `MEMORY.md` still holds a fact you corrected months
ago, that a skill's summary never made the index, that the tool you expected was
not in the catalog for that turn, or that compaction folded away the step you
were counting on. All four are invisible in the transcript and obvious in the
trace.

## What traces are not

A prompt is the most sensitive document Eggy holds: it carries `SOUL.md`,
`USER.md`, `MEMORY.md`, and your recent conversation. So traces live in the same
`eggy.db` as your messages, are served only behind the owner session, and pass
through the same secret redaction that guards durable context before they are
written.

**Nothing in the agent's own context ever reads a trace back.** A recorded prompt
cannot feed itself into the next one, which is what keeps tracing an observation
surface rather than a second memory.

Switching tracing off removes the whole capability — no recorder, no stored rows,
and this view is absent rather than empty.
