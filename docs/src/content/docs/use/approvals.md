---
title: Approvals and protected actions
description: Understand which Eggy actions require explicit owner approval and exactly what that approval authorizes.
eyebrow: Use Eggy
---

How much Eggy asks before it does something is one setting with three positions, and it applies to every tool it has.

## Modes

| `/mode` | What stops | For |
|---|---|---|
| `strict` | every tool call, reading included | watching a new setup, or a session you want to see all of |
| `normal` | anything that writes | the default, and where you should live |
| `auto` | nothing | a long batch you are supervising yourself |

Set it with `/mode strict`, `/mode normal` or `/mode auto`; a bare `/mode` reports the current one without changing it. The panel's Approvals card sets the same setting — it is one authority with two views, not two switches.

It names the mode rather than cycling to the next one. With three positions a toggle is a way to land in `auto` without having asked for it, and `auto` is the one nobody should reach by accident.

The setting is durable and survives restarts, and `/status` always names it. `approvals.mode` in `config.yaml` decides where a fresh deployment starts, but only until you choose: your choice outranks the file from then on, or `/mode` would be undone by the next restart.

## What counts as a write

Every tool declares it. Reading a file, searching mail, listing schedules and inspecting a repository change nothing outside Eggy and run without asking in `normal`. Sending mail, editing a document, writing cells, creating a schedule and curating memory all ask.

A tool that has not been classified counts as a write. That is deliberate: forgetting costs an approval prompt, while the opposite mistake costs the mutation itself.

**MCP is the exception.** A remote catalog cannot be classified from here — nothing in Eggy knows whether `deploy` writes — so an MCP server keeps its own `require_approval` list and `normal` mode does not second-guess it. `strict` still stops everything, MCP included.

## Payload-bound authorization

A protected mutation gets its own approval action and its own executor. The pending record binds to the exact normalized payload.

Approving one deletion cannot authorize a different deletion. Approving an update cannot authorize a create. A changed payload requires a new approval.

## Approval flow

1. The model calls a protected mutation tool.
2. The tool validates and normalizes the request.
3. Eggy records a pending approval and returns `awaiting_owner`.
4. The owner approves or rejects it in Telegram or web chat.
5. Eggy runs only the executor associated with that recorded action.

The model never receives mutation credentials.

## Seeing what is waiting

The web panel lists every approval still awaiting a decision, oldest first, and approves or rejects each one through the same event path a Telegram tap takes.

An approval past its 30-minute window is shown as expired rather than hidden: it stays pending in `state.json` until something decides it, which is what `status` counts, so hiding it would leave a count matching nothing visible. Approve is disabled on an expired row — the window is what authorization rests on — and rejecting one retires it.

## What is not an approval

- A `telegram_select` tap is an ordinary message.
- Adding an MCP server is a configuration-time trust decision, not a per-call approval.
- Repository inspection requires no mutation approval because the shipped repository boundary is read-only.

## Gating an MCP tool

A configured MCP server is trusted wholesale by default: its tools run when the model calls them. Name individual tools under a server's `require_approval` to change that for those tools only.

```yaml
mcp:
  servers:
    railway:
      require_approval:
        - "deploy"
        - "set-variables"
```

A gated call does not reach the server. It records an approval bound to the exact arguments, the model is told the call is waiting and to stop, and your approve tap runs it. The result is delivered to you rather than returned to the model, so gate mutations — where the answer is a confirmation — rather than reads the model needs in order to keep working.

The binding is per call: approving one `deploy` authorizes that deploy's arguments once. It does not authorize a second deploy, different arguments, or a different tool. Names are exact remote tool names, the same ones `tool_filter` takes; one that the server does not advertise is reported as a warning on the server's status rather than silently gating nothing.

## Auto mode

While `auto` is on, gated tools run immediately and return their results to the model like any other tool; no approval is recorded. Nothing selects it on your behalf, and `/status` calls it out along with the way back — a bypass you switched on and forgot is the thing worth being told about.

If you upgraded from a version with `/auto`, whatever you had it set to is carried forward: on becomes `auto`, off becomes whatever `approvals.mode` says.
