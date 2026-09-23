---
title: Telegram
description: Talk to Eggy, control the active conversation, and answer transient choices from a linked private account.
eyebrow: Use Eggy
---

Telegram is optional. Each [account](/eggy/configure/accounts/)'s
`telegram_user_id` maps that numeric sender to their account; unmapped senders
and group chats are refused, and replies, approvals and scheduled output go to
each person's own private chat. An account without a Telegram ID uses the web
panel only. In account mode, that mapping is set by each person linking their
own Telegram from the panel — see [Linking your Telegram](#linking-your-telegram)
— not by editing `config.yaml` by hand.

## Enabling Telegram

Telegram needs two credentials in the deployment environment before it can be
turned on: `TELEGRAM_BOT_TOKEN` (from [@BotFather](https://t.me/BotFather)) and
`TELEGRAM_WEBHOOK_SECRET` (a random string you choose, used only to verify that
webhook deliveries came from Telegram). Neither is ever written to
`config.yaml` or shown back by the panel.

With both provisioned, turn Telegram on from **Settings → Accounts →
Enable Telegram**, or during guided first-run setup by checking its box. Either
way, this writes `telegram.enabled: true` and nothing else — no bot is
constructed and no pairing is possible until you restart, because the adapter
and its webhook route are built once at startup like every other adapter.

### Registering the webhook

After restarting, tell Telegram where to deliver updates. Eggy does not call
`setWebhook` for you — the plan deliberately keeps this one step explicit and
in the operator's hands, so a webhook URL is never registered silently. Run
this once per deployment, from a shell that has both values:

```bash
curl "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/setWebhook" \
  --data-urlencode "url=${EGGY_PUBLIC_BASE_URL}/webhooks/telegram" \
  --data-urlencode "secret_token=${TELEGRAM_WEBHOOK_SECRET}"
```

Replace `/webhooks/telegram` with `server.telegram_webhook_path` if you changed
it from the default. Telegram's response confirms the URL it now delivers to;
`curl "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getWebhookInfo"` shows
the current registration at any time. Re-run `setWebhook` after moving hosts or
rotating the bot token.

## Linking your Telegram

Once Telegram is enabled and its webhook is registered, each person links
their own account from **Settings → Accounts**: the **Link Telegram** button
requests a single-use pairing link good for ten minutes, valid for that one
account only. Opening it and sending `/start` to the bot completes the link
immediately — no restart needed, since pairing writes only the account's
`telegram_user_id` through the same live config path the rest of the panel
uses. **Unlink Telegram** removes the mapping the same way.

There is no `@userinfobot` step and no numeric ID to find and paste: the
button is the whole flow. A pending pairing link is itself a credential —
anyone who opens it before you can claim your account's Telegram identity — so
treat it like a password link and do not forward it.

## Direct commands

Eggy's Telegram command surface is intentionally small.

| Command | Behavior |
| --- | --- |
| `/help [model\|mcp\|google\|mode]` | Show grouped commands or detailed topic help |
| `/status` | Show the active model and pending approval count |
| `/stop` | Cancel the turn currently running in this conversation |
| `/clear` | Clear recent conversation history without deleting durable memory |
| `/soul` | Show Eggy's soul (`SOUL.md`); change it by asking Eggy or in the web panel |
| `/model [alias]` | Show or select a configured alias; `default` restores the configured default |
| `/model effort [value\|default]` | Show or set your reasoning effort for the active model |
| `/model thinking [on\|off]` | Show or set delivery of provider-supplied reasoning |
| `/model settings effort\|thinking …` | Use the unambiguous settings form when an alias is named `effort`, `thinking`, or `settings` |
| `/model providers` | Name every provider and say which can be browsed |
| `/model available <provider> [filter]` | List what a provider actually serves |
| `/model add <alias> <provider> <model> [efforts]` | Write a new alias into `config.yaml` |
| `/mcp [subcommand]` | List, configure, and authorize MCP servers |
| `/google [subcommand]` | Configure and authorize Google Workspace |
| `/web` | Send a one-tap sign-in link to the web panel |
| `/mode` | Show or set how much Eggy asks before tool calls: strict, normal or auto |
| `/heartbeat [on\|off]` | Show or switch your own check-ins; off until you turn it on |
| `/restart` | Reload `config.yaml` by rebuilding the running daemon |

Unknown commands return a short `/help` pointer. Malformed known commands show
their own syntax. Ordinary text continues to the model.

`/status` also reports how many MCP servers are ready, their total tool count, and any server needing attention, and always names the approval mode in force.

`/web` replies with a link to the [web panel](/eggy/use/web-chat/) that signs you in on the way through, so opening the panel on a phone does not mean typing a password into one. The link is a credential bound to the account your Telegram chat maps to: it works once, expires five minutes after it is sent, and the browser must press **Continue** before it takes effect — that click signs you in for twelve hours and replaces whatever account was signed in before. Do not forward it: whoever uses it first is signed in as you. It is minted only from your own mapped private Telegram chat; a group chat, another chat surface, or a schedule text cannot produce one, and without `server.public_base_url` Eggy sends the plain address and you sign in by hand.

`/mode` sets how much the [approval gate](/eggy/use/approvals/) asks:

- `/mode strict` — every tool call asks first, reading included.
- `/mode normal` — native writes ask first except private memory; reads run
  freely, and MCP follows each server's approval policy. The default.
- `/mode auto` — nothing asks.

A bare `/mode` reports the current one without changing it. It names the mode rather than cycling to the next: with three of them, a toggle is a way to land in auto without having asked for it.

`/restart` applies config that was written from a chat command or the settings panel. See [Restarting to apply config](/eggy/configure/configuration/#restarting-to-apply-config) for what it does and does not restart.

## Managing MCP servers

`/mcp` is the one administration command, and it is here because the config it edits lives on the Eggy runtime: an owner holding a phone cannot shell into the deployment to add a server.

| Command | Behavior |
| --- | --- |
| `/mcp` | List configured servers with live state, tool counts, and diagnostics |
| `/mcp add <name> url=<https url> [auth=…] [transport=…] [bearer_env=VAR] [client_id=…] [client_secret_env=VAR] [enabled=…]` | Add or edit a server |
| `/mcp remove <name>` | Delete the config entry; stored OAuth credentials are kept |
| `/mcp enable <name>` / `/mcp disable <name>` | Flip one server's `enabled` flag |
| `/mcp login <name>` | Start OAuth and return the provider authorization URL |
| `/mcp login <name> <pasted redirect URL or code>` | Finish a login the browser could not deliver to the callback |
| `/mcp logout <name>` | Discard stored credentials for one server |

Edits go through the same `internal/config` helpers the web settings panel calls, under the same file lock and the same validation. There is one administration authority and two views onto it.

Three limits are deliberate:

- **No secret value is ever accepted as a chat argument.** `bearer_env` and `client_secret_env` name environment variables; the token and client secret themselves must exist in the deployment's environment. An OAuth `client_id` is accepted directly because it is not a secret — it travels in the authorization URL.
- **stdio servers are edited in `config.yaml`.** A subprocess command line and environment allowlist belong in reviewed configuration, not a chat message.
- **A config write needs a restart.** Adapters are built once at startup, so a newly added server reads as `not running — restart eggy to apply.` until then. Every write says so, and `/restart` is the restart it is asking for.

## Authorizing Google Workspace

`/google` is the same idea for the one grant that covers Gmail, Calendar, Drive,
Docs, Sheets, and Contacts. See [Google Workspace](/eggy/configure/google-workspace/).

| Command | Behavior |
| --- | --- |
| `/google` | Whether Google Workspace is authorized, and with which scopes |
| `/google set client_id=… [client_secret_env=VAR] [products=…] [enabled=…]` | Configure Google without editing `config.yaml` |
| `/google login [pasted redirect URL or code]` | Start authorization, or finish it from the paste |
| `/google logout` | Discard the stored Google grant |

## Choosing a model without leaving chat

`/model` alone reports the active alias, effective reasoning effort, and the
aliases available. Effort and thinking visibility are personal settings shared
with the web panel. Thinking visibility controls whether Eggy delivers reasoning
content supplied by the provider; it does not change the model's reasoning
capability.

The discovery subcommands let an alias be written from what a provider actually
serves, instead of from an ID copied out of a vendor's web page:

```text
/model providers
/model available openrouter sonnet
/model add sonnet openrouter anthropic/claude-sonnet-4.5 low,medium,high
/restart
/model sonnet
/model effort high
/model thinking off
```

A subcommand only wins when no configured alias answers to that name, so adding
these words cannot make an existing alias unselectable. Use `/model settings
effort …` or `/model settings thinking …` when an alias has a settings name.
`/model add` deliberately
does not select the new alias: the running daemon still holds the old catalog, so
selecting it would fail on an alias you can already see in `config.yaml`.
Listing a model does not enable it — `models` still governs what Eggy will run.
See [Model providers](/eggy/configure/model-providers/).

## Steering, stopping, and clearing

Sending an ordinary message while Eggy is working joins the turn already running
rather than queueing behind it, so a correction lands where it can still change
the next decision. `/stop` cancels the running turn; `/clear` drops the recent
window without touching durable memory. See
[Long turns, steering, and stopping](/eggy/use/long-turns/).

## Inline selections

The model can call `telegram_select` with a prompt and two to eight labelled
options. Tapping an option sends its value back as that account's next ordinary
message in the same conversation.

Each account can have its own pending selection. Selections expire after ten
minutes and are removed after use. Another account cannot consume one, and a
selection value cannot run a slash command or approve a protected action.

## Images

Send one photo or image file with an optional caption. Eggy accepts JPEG, PNG,
WebP, and GIF images up to 20 MB and sends the image to the selected model for
that turn. The selected model must support image input.

Image bytes are not retained in conversation history. To ask about the same
image in a later turn, attach it again.

## Webhook delivery

Telegram posts to `server.telegram_webhook_path`, normally `/webhooks/telegram`. The adapter validates `TELEGRAM_WEBHOOK_SECRET` and queues accepted owner messages.

An HTTP `204` proves queue acceptance only. If no reply arrives, check the daemon logs, provider credentials, and whether `EGGY_FAKE_ADAPTERS=1` was accidentally enabled outside a smoke test.
