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

Set only the credentials Eggy cannot generate on its own; you configure
everything else — the first account, the sign-in client ID, the model — from
the setup page after deploying. Railway's injected `PORT` overrides
`server.listen`, and `RAILWAY_PUBLIC_DOMAIN` is picked up automatically for
`server.public_base_url`.

| Variable | Purpose |
| --- | --- |
| `EGGY_ENCRYPTION_KEY` | Base64-encoded 32-byte key; seals sessions and grants. Generate with `openssl rand -base64 32` and keep it stable. |
| `EGGY_GOOGLE_LOGIN_CLIENT_SECRET` | The **Web application** sign-in client's secret. Name it whatever you like — you tell setup the variable name, not the value. |
| `DEEPSEEK_API_KEY` | Your model provider's API key. Same naming freedom as above. |

Optional, only once you enable Telegram from Settings after setup:

```text
TELEGRAM_BOT_TOKEN
TELEGRAM_WEBHOOK_SECRET
```

Optional, for the Workspace grant: `GOOGLE_CLIENT_SECRET` (the Desktop
client's secret). The Desktop client ID and products are set from Settings
after the first sign-in, so nothing else is needed now.

### Headless setup (alternative)

Setting `EGGY_ACCOUNTS` (as `id:google_email[:telegram_user_id]`,
comma-separated), `EGGY_GOOGLE_LOGIN_CLIENT_ID`, and
`EGGY_GOOGLE_EXPECTED_EMAIL` skips the setup page and generates
`config.yaml` on first boot instead, for scripted provisioning. Do **not**
set `EGGY_UI_USER_EMAIL` or `EGGY_UI_PASSWORD` alongside it: with accounts
there is no password login, and a config with both is refused.

## 4. Sign in

Open the service's domain. With no `EGGY_ACCOUNTS` set, Railway's deploy logs
show a one-time setup URL — open it, fill in your account, the sign-in client
ID, and the model, and **Validate and start**. The page then shows
**Sign in with Google**; sign in with the address you gave setup. The first
sign-in enrolls the account and binds it to your Google identity.

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

1. **Settings → Accounts → Add an account.** Give them a short ID and the
   Google address they will sign in with. Save — access changes immediately,
   no restart needed.
2. If the consent screen is *External / Testing*, add their address as a test
   user in Google Cloud.
3. Send them the panel address. They sign in with Google with that address;
   the first sign-in enrolls them. They start with empty conversations and
   memory, their own `/mode` and `/model`, and the shared Google connection.
4. If they use Telegram, they link it themselves from **Settings →
   Accounts → Link Telegram** — see
   [Linking your Telegram](/eggy/use/telegram/#linking-your-telegram). There
   is no numeric ID to look up by hand.

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
