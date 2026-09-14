---
title: Long turns, steering, and stopping
description: Correct Eggy mid-task, stop a running turn, and understand what a long turn keeps and what it folds away.
eyebrow: Use Eggy
---

A turn ends when the model stops calling tools — not when it has done a certain
amount of work. A job that takes forty tool calls is one turn, and you can talk
to it while it runs.

## Steering a running turn

Send an ordinary message while Eggy is working and it joins the turn already in
progress instead of queuing behind it. There is no command and no special
syntax: "actually use the staging project", sent mid-task, is a steer.

Follow-ups add to the active task by default. For example, "compare these two
documents" followed by "also include the dates" keeps the comparison and adds
the dates. Corrections replace only the conflicting instruction; a status
question does not cancel the work. Eggy is instructed to abandon the original
task only when you clearly cancel it or ask for an incompatible new objective.

It lands at the next **step boundary** — after the tool results from the current
step are in the live context, before the model is asked what to do next — which
is the only point at which a correction can change the next decision rather than
race one.

Three properties are worth knowing:

- **Steering is silent.** You get no separate acknowledgement, because the turn's
  own reply is the only thing that can say anything true about what the steer
  did.
- **A steer is never dropped.** One that arrives after the turn's last step — too
  late to be read — is not discarded. It is delivered as a turn of its own, so
  the message you sent always gets an answer.
- **A steer is never summarized away.** When a long turn compacts, your words are
  kept verbatim and moved to sit directly after the checkpoint. The instructions,
  the request, and the latest correction are the last things a turn should lose.

Only direct owner turns are steerable. A scheduled turn and a heartbeat beat are
deliberately not: they run when you are not present, and a message arriving
during one would steer work you never reviewed.

## Stopping

`/stop` cancels the turn running in that conversation. Cancellation is checked at
the step boundary as well as passed to the model and tools, so a tool that
ignores cancellation still cannot carry the turn past its current step.

`/clear` is the other end: it drops the recent conversation window for that
conversation without touching durable memory, and starts a new trace group.

## What a long turn remembers

Eggy runs each turn against one **context budget** rather than a cap on how many
tools it may call. When the exchange the loop itself produced outgrows that
budget, the oldest steps are folded into a running **checkpoint** — a factual
summary of what was looked up, what came back, and what failed — and the turn
keeps going.

Two things are never folded away:

- **Preserved input**: the instructions, durable context (`SOUL.md`, `USER.md`,
  `MEMORY.md`), the recent conversation, and your request.
- **Steering**: every owner message that arrived mid-turn, verbatim, in arrival
  order.

Everything the loop produced — assistant messages with tool calls, and the tool
results answering them — is foldable. Steps are folded whole, never a lone tool
result, because a provider rejects a tool message whose originating call is no
longer in context.

The budgets are fixed, not configuration: roughly 96,000 characters of live
loop-generated tail, the most recent 16 steps kept live, single message excerpts
of 8,192 characters in the checkpoint, and a 320,000-character ceiling on the
whole outgoing request with 16,000 held back for the answer. They count
characters rather than tokens, because characters are the only thing Eggy can
count without a provider's tokenizer, so they are deliberately conservative.

A runaway guard of 500 steps remains, but it is a guard against a model that
calls tools forever without answering — a malfunction — not a limit on how much
work a turn may do.

### When the input genuinely does not fit

If what cannot be compacted — instructions, durable context, your request and
steering, plus the newest step — does not fit the budget on its own, the turn
fails with a clear error instead of sending a request the provider would reject
or truncate. The usual causes are a `MEMORY.md` that has grown without pruning or
a single enormous tool result. Trim the document, or narrow the call.

## Reading it back

Every one of these decisions is visible in [Traces](/eggy/use/traces/): the
checkpoint appears in the prompt of the model call that followed it, and each
steer appears right after it.
