---
title: Approvals and protected actions
description: Understand which Eggy actions require explicit owner approval and exactly what that approval authorizes.
eyebrow: Use Eggy
---

Eggy ships no protected action today. Native Google Calendar was the only one, and it was removed on 2026-07-31 in favour of a configured MCP calendar server; the approval mechanism below survives it, unused, for the next mutating capability.

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

`/auto` switches every gate off, and `/auto` again switches it back on. The panel's Approvals card has the same toggle. Either way the reply names the resulting state — "Auto mode enabled." or "Auto mode disabled."

While it is on, gated tools run immediately and return their results to the model like any other tool; no approval is recorded. The setting is durable, so it survives a restart, and `status` reports it whenever it is on — a bypass you switched on and forgot is the thing worth being told about.
