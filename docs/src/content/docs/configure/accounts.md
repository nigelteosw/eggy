---
title: Accounts
description: Run one Eggy for two or more people with private username/password accounts, while Eggy uses its own Google Workspace identity.
eyebrow: Configure
---

One Eggy can serve several people. Each person is an **account**: they sign in
with a private username and password (or a one-tap Telegram link), and their
conversations, memory, watch list, schedules, traces, approvals, and `/mode`
and `/model` choices are theirs alone. What is shared is `SOUL.md`, the
reviewed skills, the configuration, and the one Google Workspace connection —
which belongs to Eggy, not to any of them.

There are no roles. Every account can change every setting, restart Eggy,
manage MCP servers and the Google connection. The only thing that separates
accounts is who owns which private record.

## What you need

- `EGGY_ENCRYPTION_KEY`, which seals sessions, credentials, and grants.
- The environment credentials `EGGY_UI_USER_EMAIL` and `EGGY_UI_PASSWORD`:
  they stay the sign-in of one named account — the operator's — and everyone
  else gets a local password set from the People card.
- A Google Workspace user for Eggy itself — say `eggy@yourdomain` — with its
  own mailbox and calendar, only if you connect the shared Google integration.
  This is an ordinary user you create in Google Admin, not a service account,
  and Eggy uses no domain-wide delegation.

No Google client is needed for sign-in. The only OAuth client in an accounts
deployment is the Desktop client for Eggy's own Workspace grant, if you use
it.

## Configure

```yaml
accounts:
  - id: nigel
    telegram_user_id: 42
  - id: partner
web:
  password_account_id: nigel
google:
  enabled: true
  client_id: desktop.apps.googleusercontent.com
  client_secret_env: GOOGLE_CLIENT_SECRET
  expected_email: eggy@example.com
  products: [calendar, gmail]
```

- `id` is a short name that becomes a directory under `accounts/` and the key
  on every private record. It cannot be renamed, and a removed one is never
  reissued.
- `telegram_user_id` maps a numeric Telegram user to this account, never a
  username. It is set by that person linking their own Telegram from
  **Settings → Accounts** — see [Linking your Telegram](/eggy/use/telegram/#linking-your-telegram)
  — or when the account is created; it can also be given from the People card.
  Someone who only uses the web panel leaves it unset; their scheduled output
  stays in their own history.
- `discord_user_id` maps a Discord user to this account the same way, by
  that person linking their own Discord from **Settings → Accounts** — see
  [Discord](/eggy/configure/discord/). It is bound separately from Telegram;
  the two never imply each other.
- `web.password_account_id` names the one account the `EGGY_UI_USER_EMAIL` /
  `EGGY_UI_PASSWORD` environment credentials sign in. With a single account it
  can be left unset — the account binds itself. With more than one it is
  required: which person the operator's password belongs to is a decision, not
  a list position.
- `google.expected_email` names Eggy's own Workspace user, when Google is
  enabled. It is Eggy's outbound identity only, and has nothing to do with
  signing in.

The `accounts` list and the single-owner `owner`/`telegram.owner_id` shape are
exclusive. Passwords are never stored in `config.yaml`: each account except
the environment-bound one has its hash in `eggy.db`, set and changed from the
People card.

On a fresh deployment, first boot writes this section from `EGGY_ACCOUNTS`
(`id[:telegram_user_id]`, comma-separated) and `EGGY_OWNER_ID` (which entry the
environment credentials sign in, required with more than one); see the
[Railway](/eggy/get-started/deploy-railway/) and
[Quickstart](/eggy/get-started/quickstart/) guides.

Everything here is operable from **Settings** as well: adding and removing
people, resetting passwords, revoking sessions, and linking channels. Changes
to the list take effect without a restart; removing someone signs them out
immediately.

## Signing in

The panel's login form takes a username and password. The username is an
account ID, or the environment alias (`EGGY_UI_USER_EMAIL`'s value) for the
account named by `web.password_account_id` — that account's password is
`EGGY_UI_PASSWORD`, managed in the deployment environment, and it can never
also carry a local password. Every other account's password is set from the
People card and lives hashed in `eggy.db`: PBKDF2-HMAC-SHA256, 600,000
iterations, a per-password salt. Nothing in a log, trace, or the config ever
holds the value.

From Telegram, `/web` in a mapped private chat replies with a one-tap link:
it works once, expires five minutes after it is sent, and the browser must
confirm it with an explicit click before it takes effect. See
[Telegram](/eggy/use/telegram/#direct-commands). The link belongs to the
account that asked for it; opening it signs in as that account, replacing
whatever was signed in before.

Sessions are opaque random tokens; only a hash is stored, with the account and
a 12-hour expiry. Sign-out revokes the session. Removing an account, resetting
its password, or revoking its sessions ends every session it has, kills its
unused sign-in links, and closes its open chat streams.

### Passwords and recovery

- **Your own password** — change it from your row on the **People** card; it
  asks for the current one, and you are signed out everywhere afterward.
- **Someone else's** — any trusted user can reset any other account's password
  from the People card without knowing the old one.
- **Revoke sessions** — ends an account's sessions and links everywhere; for
  the environment-bound account this is the manual invalidation operation,
  since its password is the environment's to rotate.
- **A removed username** is never reissued, even after cleanup failures: the
  credential row is retained and refuses a new account with that ID.

## Migrating from Google Sign-In

Earlier versions signed accounts in with Google. Moving to local accounts is
an explicit, one-command cutover that backs up first:

```sh
# with eggyd stopped and the home reachable
eggyd --home /path/to/home --migrate-local-login --password-account nigel
```

`--password-account` names the account the existing `EGGY_UI_USER_EMAIL` /
`EGGY_UI_PASSWORD` credentials will sign in — usually you. The command checks
that `eggyd` is stopped, verifies the whole candidate config, writes
`config.yaml.pre-local-login` and `eggy.db.pre-local-login` beside the
originals, migrates the database, seeds the credential rows, and writes the
new config. Everyone except the named account signs in the first time with a
password you set for them from the People card.

An interrupted migration is safe to rerun: it resumes from where it stopped
rather than starting over, and it never re-signs-everybody-out just because a
marker exists. It exits without starting Eggy; start the daemon normally
afterward.

**Rollback:** stop the new binary, restore both paired backups
(`config.yaml.pre-local-login` to `config.yaml`, `eggy.db.pre-local-login` to
`eggy.db`), restore the old binary, and start. Do not run the old binary
against the migrated database. Anything written after the cutover is not in
the backups; rolling back later is an operator decision, not automatic
recovery.

## Eggy's Google connection

Any account can connect, reconnect, or disconnect the shared Google grant
with `/google login` in chat. On the consent screen, sign in **as Eggy's
Workspace user** — the one named by `google.expected_email`. Eggy asks Google
whose account the new token is before storing it; a token for any other
address (a personal Gmail tapped through by habit) is refused and the
existing connection is left exactly as it was.

The privacy boundary is simple to state: **Eggy never holds a grant on a
person's own Google account.** People share calendars and files with, or
forward mail to, Eggy's address, and whatever Eggy can see that way is visible
to every Eggy user. The panel and `/google` say "shared with all Eggy users"
beside the connection for that reason. A private chat with Eggy does not make
a Google operation or its result private.

Every reconnect or disconnect advances the connection's generation. Approvals
bind to the generation they were granted under, so an approval from before a
reconnect is refused rather than executed against a different identity.

## Recovery

If `config.yaml` stops loading, [safe mode](/eggy/operate/safe-mode/) still
lets the environment-bound account in with the same username and password, as
long as the account list and the credential database can still be read. Local
passwords need the database, so they are not a fallback for a broken one. If
even the identity configuration cannot be established, safe mode answers only
the health probes and the file has to be repaired on the host.

Back up the whole home before changing the account list on a deployment with
history: rolling back means restoring that backup, not only an older binary.
