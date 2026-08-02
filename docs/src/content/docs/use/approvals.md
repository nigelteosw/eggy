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

> MCP tools have no per-call approval gating. A configured calendar server can create, move, and delete real events without prompting — that is the accepted cost of removing the native adapter, and adding `require_approval` to the MCP layer is what would undo it.
