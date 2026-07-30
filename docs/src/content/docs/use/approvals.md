---
title: Approvals and protected actions
description: Understand which Eggy actions require explicit owner approval and exactly what that approval authorizes.
eyebrow: Use Eggy
---

Native Google Calendar mutations are protected actions. Reading calendars is direct; creating, updating, moving, or deleting an event pauses and asks the owner.

## Payload-bound authorization

Every Calendar mutation has its own approval action and executor. The pending record binds to the exact normalized payload: calendar, event, times, and changed fields.

Approving one deletion cannot authorize a different deletion. Approving an update cannot authorize a create. A changed payload requires a new approval.

## Approval flow

1. The model calls a Calendar mutation tool.
2. The tool validates and normalizes the request.
3. Eggy records a pending approval and returns `awaiting_owner`.
4. The owner approves or rejects it in Telegram or web chat.
5. Eggy runs only the executor associated with that recorded action.

The model never receives mutation credentials.

## What is not an approval

- A `telegram_select` tap is an ordinary message.
- Adding an MCP server is a configuration-time trust decision, not a per-call approval.
- Repository inspection requires no mutation approval because the shipped repository boundary is read-only.

> Do not replace native Calendar with a trusted-wholesale MCP server unless Eggy first gains per-call approval gating for MCP tools.
