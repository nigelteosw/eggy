---
title: Web chat
description: Use Eggy's authenticated browser interface for conversations, approvals, and runtime configuration.
eyebrow: Use Eggy
---

The embedded web application is an optional owner surface served by `eggyd`. It combines multi-thread chat with the only runtime administration panel.

## Enable login

Set all three variables:

```dotenv
EGGY_UI_USER_EMAIL=owner@example.com
EGGY_UI_PASSWORD=use-a-password-manager
EGGY_ENCRYPTION_KEY=base64-encoded-32-byte-key
```

Sessions use a signed, HTTP-only cookie with a 12-hour lifetime. Failed logins are throttled by client address. Set `server.trusted_proxy_hops: 1` behind Railway so Eggy uses the address observed by the proxy; leave it at `0` when exposed directly.

## Conversations

After login you can:

- create and switch among conversation threads;
- rename a thread, overwriting the title auto-generated from its first message;
- delete a thread along with its messages — unless a workspace is still attached, which must be closed first so the checkout is not orphaned on disk;
- load durable message history;
- stream an in-progress assistant reply;
- send a new owner message;
- approve or reject a pending protected action.

Conversation threads are owner-scoped. Successful direct turns are stored in `eggy.db`.

## Configuration panel

The settings panel reads and updates:

- providers;
- model aliases and supported reasoning effort values;
- MCP server definitions;
- Google Workspace;
- the heartbeat interval and instruction;
- the merged tool catalog, read-only;
- the schedules that exist, with a cancel button on each.

Changes are written to `config.yaml`. Restart `eggyd` to reconstruct adapters and apply them. Secrets are never returned through the configuration API.

Two of those sections are not config and do not need a restart. The tool list is read live from the registry the agent loop itself runs on, so an MCP server that reconnects or is logged out of changes the page for the same reason it changes the next turn. Cancelling a schedule removes it from `cron/` and takes effect immediately.

> Configuration is an administration action, not an agent tool. The model cannot call the web settings endpoints.
