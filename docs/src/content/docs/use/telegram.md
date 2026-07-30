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

Unknown commands return the same command reference. Ordinary text continues to the model.

## Inline selections

The model can call `telegram_select` with a prompt and two to eight labelled options. Tapping an option sends its value back as the owner's next ordinary message.

Selections expire after ten minutes and are removed after use. They are a conversational convenience only: a selection cannot approve a protected action.

## Webhook delivery

Telegram posts to `server.telegram_webhook_path`, normally `/webhooks/telegram`. The adapter validates `TELEGRAM_WEBHOOK_SECRET` and queues accepted owner messages.

An HTTP `204` proves queue acceptance only. If no reply arrives, check the daemon logs, provider credentials, and whether `EGGY_FAKE_ADAPTERS=1` was accidentally enabled outside a smoke test.
