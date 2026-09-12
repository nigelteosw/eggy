---
title: Deploy on Railway
description: Run one durable Eggy daemon on Railway with a mounted home directory, Google sign-in for its people, and Eggy's own Google Workspace identity.
eyebrow: Get started
---

Eggy's container and `railway.toml` are designed for a single Railway replica.
Durable state belongs on a Railway volume mounted at `/data`. This guide sets
up a fresh deployment with [accounts](/eggy/configure/accounts/): each person
signs in with their own Google account, and Eggy connects to Google Workspace
as its own user.

## 1. Google, before Railway

Do this first; the deployment needs the values.

1. **Create Eggy's own Workspace user** in Google Admin, for example
   `eggy@yourdomain`. An ordinary user with a mailbox and calendar, not a
   service account. This is who Eggy *is* on Google; everyone shares it.
2. In a Google Cloud project, configure the **OAuth consent screen**. With an
   *Internal* audience anyone in your domain can sign in; with *External* in
   *Testing*, every person (and Eggy's own user) must be added as a test user.
3. Create a **Web application** OAuth client for sign-in. Add the redirect URI
   `https://<your-railway-domain>/auth/google/callback` — you can fill the
   domain in after step 2 below and redeploy nothing; only the client's
   redirect list changes. Note its client ID and secret.
4. Create a **Desktop app** OAuth client for Eggy's Workspace grant and enable
   the APIs for the products you want, per the
   [Google Workspace guide](/eggy/configure/google-workspace/). Note its client
   ID and secret. The two clients are never interchangeable.

## 2. Create the service

Connect the GitHub repository to a Railway service. Railway builds the root
`Dockerfile`, starts `eggyd`, and checks `GET /healthz`. Keep it at one
replica. Create a volume and mount it at `/data`: losing it loses
configuration, sessions, OAuth grants, memory, schedules, skills and logs.

Generate a public domain for the service and use it for the sign-in client's
redirect URI above.

## 3. Variables

On first boot, when `/data/config.yaml` does not exist, Eggy generates it from
these. Later changes come from **Settings**, not from re-setting variables.

| Variable | Purpose |
| --- | --- |
| `EGGY_ACCOUNTS` | The people, as `id:google_email[:telegram_user_id]`, comma-separated. Example: `nigel:nigel@example.com:123456789,partner:partner@example.com` |
| `EGGY_GOOGLE_LOGIN_CLIENT_ID` | The **Web application** client ID people sign in with |
| `EGGY_GOOGLE_LOGIN_CLIENT_SECRET` | Its secret |
| `EGGY_GOOGLE_EXPECTED_EMAIL` | Eggy's own Workspace address, e.g. `eggy@yourdomain` |
| `EGGY_ENCRYPTION_KEY` | Base64-encoded 32-byte key; seals sessions and grants. Keep it stable |
| `DEEPSEEK_API_KEY` | The generated config's default provider credential |
| `EGGY_PUBLIC_BASE_URL` | Only if Railway does not inject `RAILWAY_PUBLIC_DOMAIN` |

Generate the key with `openssl rand -base64 32`. Railway's injected `PORT`
overrides `server.listen`.

Optional, for Telegram (any account with a `telegram_user_id`):

```text
TELEGRAM_BOT_TOKEN
TELEGRAM_WEBHOOK_SECRET
```

Optional, for the Workspace grant: `GOOGLE_CLIENT_SECRET` (the Desktop
client's secret). The Desktop client ID and products are set from Settings
after the first sign-in, so nothing else is needed now.

Do **not** set `EGGY_UI_USER_EMAIL` or `EGGY_UI_PASSWORD`: with accounts there
is no password login, and a config that has both is refused.

## 4. Sign in

Open the service's domain. The page shows **Sign in with Google**; sign in
with the address you listed in `EGGY_ACCOUNTS`. The first sign-in enrolls the
account and binds it to your Google identity.

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

1. **Settings → Accounts → Add an account.** Give them a short ID, the Google
   address they will sign in with, and — if they use Telegram — their numeric
   Telegram user ID (send `/start` to `@userinfobot` to find it). Save.
2. If the consent screen is *External / Testing*, add their address as a test
   user in Google Cloud.
3. **Restart Eggy** (Settings → Advanced, or `/restart` in chat). The account
   list is read at startup.
4. Send them the panel address. They sign in with Google with that address;
   the first sign-in enrolls them. They start with empty conversations and
   memory, their own `/mode` and `/model`, and the shared Google connection.

To remove someone, **Remove** on their row: they are signed out immediately
and can no longer sign in; their private history stays in the database. If
someone needs to switch Google accounts, edit their address after
**Reset binding**, which signs them out until they re-enroll.

## 6. Verify

Check `/healthz`, then `/readyz`. For Telegram, a webhook returning `204`
means the update entered Eggy's queue; use Railway logs and a real message
from a listed sender for end-to-end verification. If `config.yaml` ever stops
loading, [safe mode](/eggy/operate/safe-mode/) still lets an account in with
Google to repair it.
