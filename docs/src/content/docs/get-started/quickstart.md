---
title: Quickstart
description: Build Eggy, run it locally with local account login, and start your first conversation.
eyebrow: Get started
---

This guide runs Eggy directly from a checkout on your own machine. It uses the
same daemon and home layout as a hosted deployment, so what you set up here
carries over to [Railway](/eggy/get-started/deploy-railway/).

## Prerequisites

- Go 1.26, Bun, Git
- An API key for at least one model provider
- A username and password for your own account
- Optional: a Google Workspace user for Eggy itself, a Desktop OAuth client,
  a Telegram bot token and webhook secret

## Build

```sh
git clone https://github.com/nigelteosw/eggy.git
cd eggy
make build
```

`make build` compiles the web application under `website/`, embeds it in the
Go binary, and writes `bin/eggyd`.

## Provision credentials

Guided setup writes `config.yaml` for you from the web panel, but the
credentials it needs have to already exist in the environment — the setup
form only ever collects environment *variable names*, never secret values.
Create `.env` in the checkout:

```dotenv
EGGY_ENCRYPTION_KEY=...   # openssl rand -base64 32; paste the value, keep it stable
DEEPSEEK_API_KEY=...      # or whichever provider you plan to use
EGGY_UI_USER_EMAIL=...    # your username on the login form
EGGY_UI_PASSWORD=...      # your password; rotate it here, then restart
```

`EGGY_PUBLIC_BASE_URL` is optional for local use — Eggy falls back to
`http://localhost:8080` — but set it if you're running behind a different
host or port. See [Environment variables](#environment-variables) below for
the complete picture, including what's only needed for Telegram or
repositories.

## Start Eggy

```sh
./bin/eggyd --home "$PWD/data"
```

The daemon reads `.env` from the home, creates its owned directories, opens
the database, and starts listening on `:8080`. With no `config.yaml` yet and
none of the [headless variables](#headless-setup-alternative) set, it prints
a one-time setup URL to the console instead of starting normally:

```text
Eggy setup URL (valid for 30 minutes): http://localhost:8080/#setup=...
```

## Complete guided setup

Open that URL. It signs you into the setup page automatically — the token is
in the link's fragment, never sent as a query parameter a proxy might log.
Fill in:

- **Account** — an ID for yourself (your username), and optionally the
  numeric Telegram ID you'll link later
- **Model** — the provider, its base URL, and the variable name holding its
  API key (`DEEPSEEK_API_KEY` above)
- **Telegram** (optional) — check the box only if `TELEGRAM_BOT_TOKEN` and
  `TELEGRAM_WEBHOOK_SECRET` are already set; see
  [Telegram](/eggy/use/telegram/#enabling-telegram)

**Validate and start** checks every named variable is actually present before
writing anything; a missing one is reported inline rather than failing after
the fact. Once setup completes, Eggy reloads with the config it just wrote
and the page takes you to sign-in automatically.

```sh
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Sign in

Open `http://localhost:8080/` and sign in with the username and password from
your environment (`EGGY_UI_USER_EMAIL` / `EGGY_UI_PASSWORD`). Your
conversations, memory and settings are under `data/accounts/you/`.

Browsers accept Eggy's `Secure` session cookie on `localhost` over plain
`http`; on any other hostname you need HTTPS.

## Connect Google Workspace (optional)

Under **Settings → Connections → Google Workspace**, set the Desktop client
ID, the secret's variable name, and the products; save and restart. Run
`/google login` in chat and, on the consent screen, sign in **as Eggy's own
Workspace user** — the expected address — never as yourself. Eggy verifies
whose account the grant is and refuses any other. Then share calendars or
files with, or forward mail to, that address.

## Add a second person

1. **Settings → People → Add an account** with their ID and a password you
   hand them by hand. Access changes immediately; no restart needed.
2. They open the same address and sign in with that username and password.
   They get their own empty history, memory, and personal settings.
3. If they use Telegram, they link their own chat from their row on the same
   card, or send themselves a `/web` link once linked.

Removing someone from the same card signs them out at once, and their
username is never reissued. Full details in
[Accounts](/eggy/configure/accounts/).

## Telegram (optional)

Telegram needs a public HTTPS webhook to deliver updates, so it's mostly for
the hosted deployment; locally, use the web panel instead. See
[Enabling Telegram](/eggy/use/telegram/#enabling-telegram) for provisioning
the bot credentials, turning it on from Settings, and registering the
webhook, and [Linking your Telegram](/eggy/use/telegram/#linking-your-telegram)
for how each person connects their own chat — there is no numeric ID to look
up by hand.

## Environment variables

Guided setup only ever needs three credentials to exist in the environment
beforehand — everything else is either a plain form field (never a secret) or
only required once you turn on the capability that needs it:

| Variable | When it's needed |
| --- | --- |
| `EGGY_ENCRYPTION_KEY` | Always. Seals sessions, account credentials, and the Google Workspace grant. |
| Your provider's API key (e.g. `DEEPSEEK_API_KEY`) | Always. Name it as the "API key variable" during setup. |
| `EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD` | Always. The first account's sign-in; the only password the environment manages. |
| `EGGY_PUBLIC_BASE_URL` | Recommended once you're not on `localhost`; auto-detected on Railway. |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET` | Only if you enable Telegram. |
| `GITHUB_TOKEN` | Only once a repository is configured. |
| A Workspace client secret (your own name, e.g. `GOOGLE_CLIENT_SECRET`) | Only once you connect Eggy's Google Workspace identity from Settings. |
| `TAVILY_API_KEY` | Only if you enable web search. |

The variable *names* above are the defaults the setup form and
`.env.example` suggest; you can use any name you like, since `config.yaml`
only ever stores which variable to read from, never the value.

## Headless setup (alternative)

Setting any of `EGGY_ACCOUNTS`, `EGGY_OWNER_ID`, or `EGGY_TELEGRAM_OWNER_ID`
skips the setup page entirely: Eggy generates `config.yaml` from those
variables on first boot instead, the way earlier versions always did. `EGGY_ACCOUNTS`
lists the people as `id[:telegram_user_id]`, comma-separated, and with more
than one entry `EGGY_OWNER_ID` must name which of them the `EGGY_UI_*`
credentials above sign in. See `.env.example` for the full set this path
reads. Useful for scripted or non-interactive provisioning; the guided page
above is otherwise the better default.
