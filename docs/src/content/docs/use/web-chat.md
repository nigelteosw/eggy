---
title: Web chat and settings
description: Use Eggy's authenticated browser interface for conversations, trace inspection, approvals, and runtime configuration.
eyebrow: Use Eggy
---

The embedded web application is an optional owner surface served by `eggyd`
itself — one binary, no separate front end to deploy. It has three views, chosen
from the top navigation and addressable directly:

| View | Path | What it is |
| --- | --- | --- |
| Chat | `/` | Multi-thread conversation with Eggy |
| Traces | `/traces` | [Every turn as it actually ran](/eggy/use/traces/) |
| Settings | `/settings` | The only runtime administration panel |

The layout is responsive: the same three views work on a phone, with the thread
list and the settings sidebar collapsing into their own controls.

## Enable login

With [accounts](/eggy/configure/accounts/) configured, the login page shows
**Sign in with Google** and nothing else; the variables below are not used and
must not be set. The navigation shows who is signed in with a sign-out control,
and every person sees only their own chats, traces, schedules and approvals.

For a single-owner deployment, set all three variables:

```dotenv
EGGY_UI_USER_EMAIL=owner@example.com
EGGY_UI_PASSWORD=use-a-password-manager
EGGY_ENCRYPTION_KEY=base64-encoded-32-byte-key
```

Sessions use a signed, HTTP-only cookie with a 12-hour lifetime. Failed logins
are throttled by client address. Set `server.trusted_proxy_hops: 1` behind
Railway so Eggy uses the address observed by the proxy; leave it at `0` when
exposed directly.

From Telegram, `/web` sends a one-tap sign-in link so opening the panel on a
phone does not mean typing the panel password into one. The link works once and
expires five minutes after it is sent. With accounts configured, `/web` sends
the panel address only: there is no bearer link, and the person signs in with
Google there.

## Conversations

After login you can:

- create and switch among conversation threads;
- rename a thread, overwriting the title auto-generated from its first message;
- delete a thread along with its messages — unless a workspace is still attached,
  which must be closed first so the checkout is not orphaned on disk;
- load durable message history;
- watch an in-progress assistant reply stream;
- send a new owner message, including
  [steering a turn that is already running](/eggy/use/long-turns/);
- approve or reject a pending protected action inline.

Conversation threads are owner-scoped. Successful direct turns are stored in
`eggy.db`. A new thread is not created until its first message, so the **+**
button cannot mint empty chats on top of an empty chat.

Images are a Telegram-only input; the web composer is text.

## Settings

The panel is organized into seven sections.

| Section | Contents |
| --- | --- |
| **Models** | Providers, and the aliases that route to them — including browsing a provider's live catalog to fill an alias in |
| **Connections** | MCP servers and Google Workspace, with their OAuth flows |
| **Capabilities** | The merged tool catalog, read-only |
| **Automation** | Schedules, the heartbeat, and the watch list |
| **Permissions** | The approval mode, and every approval waiting on you |
| **Appearance** | Panel theme, stored in `config.yaml` so it follows you across devices |
| **Advanced** | Tracing settings, raw `config.yaml`, and Restart |

Changes to configuration are written to `config.yaml`. Restart `eggyd` to
reconstruct adapters and apply them: the **Restart** button does this without a
redeploy, as does `/restart` in chat, and every save says so. A config Eggy could
not load is refused there rather than applied — see
[Restarting to apply config](/eggy/configure/configuration/#restarting-to-apply-config).
Secrets are never returned through the configuration API.

### What does not need a restart

Four things on the panel are not config and take effect immediately:

- **Capabilities** reads live from the registry the agent loop itself runs on, so
  an MCP server that reconnects or is logged out of changes the page for the same
  reason it changes the next turn.
- **Cancelling a schedule** removes its record at once.
- **Approvals** are read there and decided through the same event path a Telegram
  tap uses, so the panel is a second view onto one mechanism rather than a second
  way to approve something. The approval mode itself is durable runtime state,
  not config.
- **The watch list** is a Markdown document, saved directly.
- **Appearance** applies on the next page load.

> Configuration is an administration action, not an agent tool. The model cannot
> call the web settings endpoints.

## When Eggy could not start

The same bundle serves the [safe-mode](/eggy/operate/safe-mode/) repair page: the
startup error and an editor for `config.yaml`, with no chat, no agent, and no
memory behind it.
