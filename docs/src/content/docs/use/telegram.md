---
title: Telegram
description: Talk to Eggy, control the active conversation, and answer transient choices from one allowlisted Telegram account.
eyebrow: Use Eggy
---

Telegram is optional. When configured, the adapter accepts updates only from `telegram.owner_id` and maps that account to Eggy's canonical owner.

## Direct commands

Eggy's Telegram command surface is intentionally small.

| Command | Behavior |
| --- | --- |
| `/help` | List the available commands |
| `/status` | Show the active model and pending approval count |
| `/stop` | Cancel the turn currently running in this conversation |
| `/clear` | Clear recent conversation history without deleting durable memory |
| `/model [alias]` | Show or select a configured alias; `default` restores the configured default |
| `/mcp [subcommand]` | List, configure, and authorize MCP servers |

Unknown commands return the same command reference. Ordinary text continues to the model.

`/status` also reports how many MCP servers are ready, their total tool count, and any server needing attention.

## Managing MCP servers

`/mcp` is the one administration command, and it is here because the config it edits lives on the Eggy runtime: an owner holding a phone cannot shell into the deployment to add a server.

| Command | Behavior |
| --- | --- |
| `/mcp` | List configured servers with live state, tool counts, and diagnostics |
| `/mcp add <name> url=<https url> [auth=…] [transport=…] [bearer_env=VAR] [enabled=…]` | Add or edit a server |
| `/mcp remove <name>` | Delete the config entry; stored OAuth credentials are kept |
| `/mcp enable <name>` / `/mcp disable <name>` | Flip one server's `enabled` flag |
| `/mcp login <name>` | Start OAuth and return the provider authorization URL |
| `/mcp logout <name>` | Discard stored credentials for one server |

Edits go through the same `internal/config` helpers the web settings panel calls, under the same file lock and the same validation. There is one administration authority and two views onto it.

Three limits are deliberate:

- **No secret value is ever accepted as a chat argument.** `bearer_env` names an environment variable; the token itself must exist in the deployment's environment.
- **stdio servers are edited in `config.yaml`.** A subprocess command line and environment allowlist belong in reviewed configuration, not a chat message.
- **A config write needs a restart.** Adapters are built once at startup, so a newly added server reads as `not running — restart eggy to apply.` until then. Every write says so.

## Inline selections

The model can call `telegram_select` with a prompt and two to eight labelled options. Tapping an option sends its value back as the owner's next ordinary message.

Selections expire after ten minutes and are removed after use. They are a conversational convenience only: a selection cannot approve a protected action.

## Webhook delivery

Telegram posts to `server.telegram_webhook_path`, normally `/webhooks/telegram`. The adapter validates `TELEGRAM_WEBHOOK_SECRET` and queues accepted owner messages.

An HTTP `204` proves queue acceptance only. If no reply arrives, check the daemon logs, provider credentials, and whether `EGGY_FAKE_ADAPTERS=1` was accidentally enabled outside a smoke test.
