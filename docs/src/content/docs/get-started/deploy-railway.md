---
title: Deploy on Railway
description: Run one durable Eggy daemon on Railway with a mounted home directory, local accounts for its people, and Eggy's own Google Workspace identity.
eyebrow: Get started
---

Eggy's container and `railway.toml` are designed for a single Railway replica.
Durable state belongs on a Railway volume mounted at `/data`. This guide sets
up a fresh deployment with [accounts](/eggy/configure/accounts/): each person
signs in with a private username and password, and Eggy connects to Google
Workspace as its own user.

## 1. Google, before Railway — only if you use the integration

Sign-in needs no Google client at all. If you will connect Eggy's own
Workspace identity:

1. **Create Eggy's own Workspace user** in Google Admin, for example
   `eggy@yourdomain`. An ordinary user with a mailbox and calendar, not a
   service account. This is who Eggy *is* on Google; everyone shares it.
2. Create a **Desktop app** OAuth client for Eggy's Workspace grant and
   enable the APIs for the products you want, per the
   [Google Workspace guide](/eggy/configure/google-workspace/). Note its client
   ID and secret. Nothing is registered against your Railway domain.

## 2. Create the service

Connect the GitHub repository to a Railway service. Railway builds the root
`Dockerfile`, starts `eggyd`, and checks `GET /healthz`. Keep it at one
replica. Create a volume and mount it at `/data`: losing it loses
configuration, sessions, OAuth grants, memory, schedules, skills and logs.

Generate a public domain for the service; it is the address your people
sign in at.

## 3. Variables

Set only the credentials Eggy cannot generate on its own; you configure
everything else — the first account, the model — from the setup page after
deploying. Railway's injected `PORT` overrides
`server.listen`, and `RAILWAY_PUBLIC_DOMAIN` is picked up automatically for
`server.public_base_url`.

| Variable | Purpose |
| --- | --- |
| `EGGY_ENCRYPTION_KEY` | Base64-encoded 32-byte key; seals sessions, account credentials, and grants. Generate with `openssl rand -base64 32` and keep it stable. |
| `EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD` | The first account's username and password on the login form. Every other person gets a local password from the People card. Rotating this password is an environment change plus a restart. |
| `DEEPSEEK_API_KEY` | Your model provider's API key. Name it whatever you like — you tell setup the variable name, not the value. |

Optional, only once you enable Telegram from Settings after setup:

```text
TELEGRAM_BOT_TOKEN
TELEGRAM_WEBHOOK_SECRET
```

Optional, for the Workspace grant: `GOOGLE_CLIENT_SECRET` (the Desktop
client's secret). The Desktop client ID and products are set from Settings
after the first sign-in, so nothing else is needed now.

### Headless setup (alternative)

Setting `EGGY_ACCOUNTS` (as `id[:telegram_user_id]`, comma-separated) skips
the setup page and generates `config.yaml` on first boot instead, for
scripted provisioning. With more than one entry, `EGGY_OWNER_ID` must name
which of them the `EGGY_UI_*` credentials above sign in. `EGGY_ACCOUNTS` and
the single-owner `EGGY_TELEGRAM_OWNER_ID` shape are exclusive.

## 4. Sign in

Open the service's domain. With no `EGGY_ACCOUNTS` set, Railway's deploy logs
show a one-time setup URL — open it, fill in your account ID, the model, and
**Validate and start**. The page then shows the username/password form; sign
in with `EGGY_UI_USER_EMAIL` and `EGGY_UI_PASSWORD`.

Then, under **Settings → Connections → Google Workspace**, set the Desktop
client ID, `client_secret_env: GOOGLE_CLIENT_SECRET`, and the products; save
and **Restart Eggy**. In chat, run `/google login`, and on Google's consent
screen sign in **as Eggy's Workspace user** — the expected address — not as
yourself. A personal account is refused and nothing is stored. The Google card
then shows "Connected as eggy@yourdomain · Shared with all Eggy users".

Share the calendars and Drive files Eggy should see with that address, or
forward mail to it. Whatever Eggy can reach that way is visible to every Eggy
user.

## 5. Onboard another person

Everyone with an account can do this.

1. **Settings → People → Add an account.** Give them a short ID (their
   username), a password you hand them by hand, and optionally their numeric
   Telegram ID. Save — access changes immediately, no restart needed.
2. Send them the panel address. They sign in with that username and password.
   They start with empty conversations and memory, their own `/mode` and
   `/model`, and the same shared connections.
3. If they use Telegram, they link it themselves from **Settings →
   People → Link Telegram** — see
   [Linking your Telegram](/eggy/use/telegram/#linking-your-telegram). There
   is no numeric ID to look up by hand.

To remove someone, **Remove** on their row: they are signed out immediately
and can no longer sign in, and their username is never reissued. Their
private history stays in the database until you delete it.

## Migrating an existing deployment

A deployment from before local accounts — Google Sign-In — moves over with
one command, on the mounted home, with the daemon stopped:

```sh
# from a shell that can reach the volume, with eggyd stopped
eggyd --home /data --migrate-local-login --password-account <your-id>
```

`--password-account` names the account your `EGGY_UI_*` credentials sign in
after the move. The command preflights the whole candidate config, writes
`config.yaml.pre-local-login` and `eggy.db.pre-local-login` beside the
originals, migrates the database, seeds everyone's credential rows, and
exits without starting Eggy — start the deployment again normally. An
interrupted run is safe to rerun; it resumes rather than starting over.

To roll back: stop the new binary, restore both paired backups, restore the
old binary, and start it. Never run the old binary against the migrated
database. Full details in [Accounts](/eggy/configure/accounts/#migrating-from-google-sign-in).

## 6. Verify

Check `/healthz`, then `/readyz`. For Telegram, a webhook returning `204`
means the update entered Eggy's queue; use Railway logs and a real message
from a listed sender for end-to-end verification. If `config.yaml` ever stops
loading, [safe mode](/eggy/operate/safe-mode/) still lets an account in with
the same username and password to repair it.
