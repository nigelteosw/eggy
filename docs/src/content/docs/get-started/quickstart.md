---
title: Quickstart
description: Build Eggy, run it locally with Google sign-in, and start your first conversation.
eyebrow: Get started
---

This guide runs Eggy directly from a checkout on your own machine. It uses the
same daemon and home layout as a hosted deployment, so what you set up here
carries over to [Railway](/eggy/get-started/deploy-railway/).

## Prerequisites

- Go 1.26, Bun, Git
- An API key for at least one model provider
- A Google Cloud project with a **Web application** OAuth client for sign-in
  (redirect URI `http://localhost:8080/auth/google/callback`; Google allows
  plain `http` for `localhost`) — see [Accounts](/eggy/configure/accounts/)
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

## Configure

Let first boot write the config for you. Create `.env` in the checkout:

```dotenv
EGGY_ACCOUNTS=you:you@example.com
EGGY_GOOGLE_LOGIN_CLIENT_ID=xxxx.apps.googleusercontent.com
EGGY_GOOGLE_LOGIN_CLIENT_SECRET=...
EGGY_GOOGLE_EXPECTED_EMAIL=eggy@example.com   # Eggy's own Workspace user; optional until you connect Google
EGGY_PUBLIC_BASE_URL=http://localhost:8080
EGGY_ENCRYPTION_KEY=$(openssl rand -base64 32)   # paste the value, keep it stable
DEEPSEEK_API_KEY=...
```

`EGGY_ACCOUNTS` lists people as `id:google_email[:telegram_user_id]`, comma-
separated. Do not set `EGGY_UI_USER_EMAIL`/`EGGY_UI_PASSWORD`: with accounts
there is no password login.

If you would rather write the file yourself, `cp config.example.yaml
config.yaml` and edit the `accounts`, `web.google_login` and `google` sections;
the example is already in the accounts shape.

## Start Eggy

```sh
./bin/eggyd --home "$PWD/data"
```

The daemon reads `.env` from the home, generates `data/config.yaml` on first
boot, creates its owned directories, opens the database, and starts listening
on `:8080`.

```sh
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

## Sign in

Open `http://localhost:8080/`, click **Sign in with Google**, and sign in with
the address from `EGGY_ACCOUNTS`. The first sign-in enrolls the account. Your
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

1. **Settings → Accounts → Add an account** with their ID, Google address and
   optional Telegram user ID.
2. Restart Eggy.
3. They open the same address and sign in with Google. They get their own
   empty history and memory, and the shared Google connection.

Removing someone from the same card signs them out at once. Full details in
[Accounts](/eggy/configure/accounts/).

## Telegram (optional)

Give an account a `telegram_user_id` and set:

```dotenv
TELEGRAM_BOT_TOKEN=...
TELEGRAM_WEBHOOK_SECRET=...
```

Telegram needs a public HTTPS webhook, so this is mostly for the hosted
deployment; locally, use the web panel.
