---
title: Discord
description: Talk to Eggy in a private Discord DM. Add the bot from the panel; each person links their own Discord.
eyebrow: Configure
---

Eggy can talk to each account in a private Discord direct message. The bot is
added from the web panel, not from the deployment environment: paste its token
under **Settings → Connections → Discord**, restart, and each person links
their own Discord from **Settings → Accounts**.

What Discord gives you is the same personal Eggy you have on Telegram and in
the web panel: your memory, your tools, your `/mode` and `/model` choices, and
the same approval flow. What is separate is the conversation itself. A Discord
DM has its own history, apart from Telegram's thread and from every web
thread, while your personal memory is shared across all of them.

This is a private, text-only channel. Servers, threads, group DMs, and
attachments are out of scope for now; Discord shared channels are deferred
along with Telegram groups.

## What you need

- An account-mode Eggy ([Accounts](/eggy/configure/accounts/)) with
  `EGGY_ENCRYPTION_KEY` set — the bot token is sealed under it.
- A Discord application with a bot user, created in the
  [Discord Developer Portal](https://discord.com/developers/applications).
  Copy the bot token; you will not see it again after leaving the page.
- The bot invited to any server you share with it, or the application's
  **User Install** enabled, so that a DM with it can be opened. DMs need no
  server permissions and no privileged intents: message content in a DM is
  delivered without the *Message Content* intent.

## Set up the bot

1. Open **Settings → Connections → Discord**.
2. Paste the bot token, optionally the application ID (it only powers the
   "Open Discord" shortcut), turn Discord on, and save. The token is stored
   encrypted in Eggy's database and is never written to `config.yaml` or
   shown again; the card only reports whether one is set.
3. Restart Eggy (**Advanced → Restart** or `/restart`). The card shows
   *running* once the gateway is connected.

`config.yaml` records only the non-secret part:

```yaml
discord:
  enabled: true
  application_id: "123456789012345678" # optional
```

Operators who keep every secret in the environment can set
`DISCORD_BOT_TOKEN` instead; it overrides a stored token and the card says
*from environment*. It is never required at start-up: an enabled Discord
section with no token anywhere logs a warning and runs no bot.

## Link your Discord

Only a linked person can talk to the bot, and only they can link themselves.

1. In **Settings → Accounts**, click **Link Discord** in your own row. Eggy
   shows a single-use command, `/link <token>`, valid for ten minutes.
2. Open a direct message with the bot and send that command. Don't share it:
   whoever sends it first claims your account's Discord.
3. Your row shows your Discord user ID once it is bound.

**Unlink Discord** removes the binding at once: any pending token is
cancelled, the next message from that user is refused, and a turn already in
flight can no longer deliver into the DM. Removing an account or resetting
its binding cancels its tokens too.

`discord_user_id` on an account is the opaque Discord user, never a username.
It is set by linking, not by editing the file by hand, and a numeric
coincidence with a Telegram ID means nothing: the two are separate fields
bound separately.

## What the bot does with messages

- A DM from a linked person starts an ordinary owner turn, replying in the
  same DM. Replying to one of Eggy's messages quotes that passage into the
  prompt, as on Telegram; nothing else is fetched from the DM's history.
- The text commands you know from Telegram — `/mode`, `/model`, `/status`,
  `/clear`, `/restart` and the rest — work the same way.
- A DM from someone Eggy does not know gets one short refusal per ten
  minutes and nothing else. The only thing an unknown user can do is redeem
  a linking token. Their text never reaches the model, history, traces, or
  logs.
- Messages from bots, webhooks, servers, threads, and group DMs are ignored
  outright, including yours.
- Attachments are refused with a notice; send text.

## Approvals

When a turn needs your approval, the DM receives a notice with the summary
and a link to the web panel. Decide it there — Discord has no approve or
reject buttons, and `/approve` or `/reject` typed into the DM are refused.
The outcome of your decision is delivered back to the DM the approval was
raised in, even after a restart.

## Unprompted output

Scheduled runs, reminders, and the heartbeat still report to Telegram, not
to Discord. Discord is a channel you open; nothing is pushed to it that you
did not ask for in that DM.
