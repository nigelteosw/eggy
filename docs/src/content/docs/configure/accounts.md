---
title: Accounts
description: Run one Eggy for two or more people, each signing in with Google, while Eggy uses its own Google Workspace identity.
eyebrow: Configure
---

One Eggy can serve several people. Each person is an **account**: they sign in
with their own Google identity, and their conversations, memory, watch list,
schedules, traces, approvals, and `/mode` and `/model` choices are theirs
alone. What is shared is `SOUL.md`, the reviewed skills, the configuration, and
the one Google Workspace connection — which belongs to Eggy, not to any of
them.

There are no roles. Every account can change every setting, restart Eggy,
manage MCP servers and the Google connection. The only thing that separates
accounts is who owns which private record.

## What you need

- A Google Cloud project (the one from the
  [Google Workspace guide](/eggy/configure/google-workspace/) is fine).
- A **Web application** OAuth client for sign-in, with the redirect URI
  `<server.public_base_url>/auth/google/callback`. This is a different client
  from the Desktop client Eggy's own Workspace grant uses; the two are never
  interchangeable.
- The consent screen configured for whoever will sign in. With an *External*
  audience in *Testing*, every person must be listed as a test user; an
  *Internal* audience covers everyone in your Workspace domain. Publishing to
  production removes the test-user list and the seven-day refresh-token limit
  the Workspace grant would otherwise be subject to.
- A Google Workspace user for Eggy itself — say `eggy@yourdomain` — with its
  own mailbox and calendar. This is an ordinary user you create in Google
  Admin, not a service account, and Eggy uses no domain-wide delegation.
- `EGGY_ENCRYPTION_KEY`, which seals sessions and grants.

## Configure

```yaml
accounts:
  - id: nigel
    google_email: nigel@example.com
  - id: partner
    google_email: partner@example.com
web:
  google_login:
    client_id: web-login.apps.googleusercontent.com
    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET
google:
  enabled: true
  client_id: desktop.apps.googleusercontent.com
  client_secret_env: GOOGLE_CLIENT_SECRET
  expected_email: eggy@example.com
  products: [calendar, gmail]
```

- `id` is a short name that becomes a directory under `accounts/` and the key
  on every private record. It cannot be renamed.
- `google_email` is the address that may enroll the account. It is compared
  exactly (case-insensitively; dots and plus-suffixes are not collapsed). After
  the first sign-in the account is bound to Google's stable identity for that
  person, and the address only decides who may enroll an *unbound* account.
- `telegram_user_id` maps a numeric Telegram user to this account, never a
  username. It is set by that person linking their own Telegram from
  **Settings → Accounts** — see [Linking your Telegram](/eggy/use/telegram/#linking-your-telegram)
  — not by editing this field by hand. Someone who only uses the web panel
  leaves it unset; their scheduled output stays in their own history.
- `discord_user_id` maps a Discord user to this account the same way, by
  that person linking their own Discord from **Settings → Accounts** — see
  [Discord](/eggy/configure/discord/). It is bound separately from Telegram;
  the two never imply each other.
- `web.google_login` names the Web application client. The secret is read from
  the environment variable named, never written to the file.
- `google.expected_email` names Eggy's own Workspace user. Required when
  accounts are configured and Google is enabled.

The `accounts` list and the single-owner `owner`/`telegram.owner_id` shape are
exclusive. With accounts configured, `EGGY_UI_USER_EMAIL` and
`EGGY_UI_PASSWORD` must not be set: the password login stops existing, and so
does the `/web` one-tap link.

On a fresh deployment, first boot writes this section from `EGGY_ACCOUNTS`
(`id:google_email[:telegram_user_id]`, comma-separated),
`EGGY_GOOGLE_LOGIN_CLIENT_ID`, `EGGY_GOOGLE_LOGIN_CLIENT_SECRET` and
`EGGY_GOOGLE_EXPECTED_EMAIL`; see the
[Railway](/eggy/get-started/deploy-railway/) and
[Quickstart](/eggy/get-started/quickstart/) guides.

Everything here is operable from **Settings → Accounts** as well: adding,
editing and removing people, resetting a binding, the sign-in client, and the
expected Google account. Changes to the list take effect on restart; removing
someone signs them out immediately.

## Signing in

The panel shows one control: **Sign in with Google**. The browser goes to
Google and comes back to `/auth/google/callback` with a single-use state bound
to that browser. Eggy verifies the ID token — signature, issuer, audience,
expiry, nonce, and that Google vouches for the address — and then:

- an identity already bound to an account signs in as that account;
- an unbound identity whose verified address matches an unenrolled account
  enrolls it and signs in;
- anything else lands on the login page with a generic failure. Which check
  refused it is logged, never shown.

Sessions are opaque random tokens; only a hash is stored, with the account and
a 12-hour expiry. Sign-out revokes the session. Removing an account or
resetting its binding revokes every session it has and closes its open chat
streams.

### Changing someone's address

An enrolled account's address is pinned. To move an account to a different
Google identity, use **Reset binding** on the Accounts card: the person is
signed out everywhere and the next sign-in with the configured address enrolls
them again. Editing the address alone is refused until the binding is reset,
so a typo can never hand an account to someone else.

## Eggy's Google connection

Any account can connect, reconnect, or disconnect the shared Google grant with
`/google login` in chat. On the consent screen, sign in **as Eggy's Workspace
user** — the one named by `google.expected_email`. Eggy asks Google whose
account the new token is before storing it; a token for any other address (a
personal Gmail tapped through by habit) is refused and the existing connection
is left exactly as it was.

The privacy boundary is simple to state: **Eggy never holds a grant on a
person's own Google account.** People share calendars and files with, or
forward mail to, Eggy's address, and whatever Eggy can see that way is visible
to every Eggy user. The panel and `/google` say "shared with all Eggy users"
beside the connection for that reason. A private chat with Eggy does not make a
Google operation or its result private.

Every reconnect or disconnect advances the connection's generation. Approvals
bind to the generation they were granted under, so an approval from before a
reconnect is refused rather than executed against a different identity.

## Recovery

If `config.yaml` stops loading, safe mode still lets an account in through
Google Sign-In, as long as the accounts list, the sign-in client, and the
public base URL can still be read from the broken file and the session
database opens. There is no password to fall back to. If even the identity
configuration cannot be established, safe mode answers only the health probes
and the file has to be repaired on the host.

Back up the whole home before changing the account list on a deployment with
history: rolling back means restoring that backup, not only an older binary.
