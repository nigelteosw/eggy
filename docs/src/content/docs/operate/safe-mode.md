---
title: Safe mode
description: Repair a config that will not load from the browser, without a shell and without a redeploy.
eyebrow: Operate
---

When startup fails, `eggyd` does not exit. It serves a repair page instead.

The failure this exists for is specific: `config.yaml` lives on a mounted volume
that only the daemon can reach, and a container that crash-loops has no shell to
reach it with. Exiting on a bad config is therefore unrecoverable on exactly the
deployment Eggy is built for. So the process stays up with one job — let the
owner read the error and fix the file that caused it.

## What it looks like

Safe mode is the same web bundle as always, compiled into the binary, rendering
its repair screen instead of chat. After signing in you get:

- the startup error, verbatim — it is the whole reason the surface exists;
- an editor for `config.yaml`, loaded from disk;
- a save that writes and then hands control back.

## What it serves

| Route | Behavior |
| --- | --- |
| `/healthz` | Still `200`. The process is alive, and a platform that reroutes away from an unhealthy container would take the repair page down with it |
| `/readyz` | `503` with the failure. The deployment is genuinely not serving |
| `/` and login | The repair page, behind the owner session |
| everything else | Unavailable |

There is no chat, no agent, no memory, and no Telegram. The only state safe mode
can change is `config.yaml`, and only through a body that the normal config
loader has already accepted.

## Getting back out

**A saved config is only written once it loads.** The candidate is run through
the same load startup uses, with the same environment including `.env`; if it
fails, the file on disk is untouched and you see the new error. That is what
makes it impossible to lock yourself out with a second bad config.

Once one does load, Eggy retries startup in the same process — the supervisor
loop that `/restart` also uses. Nothing redeploys and the container is never
replaced.

## What it needs

Safe mode reads its credentials from the environment, not from the config that
failed:

```dotenv
EGGY_UI_USER_EMAIL=owner@example.com
EGGY_UI_PASSWORD=use-a-password-manager
EGGY_ENCRYPTION_KEY=base64-encoded-32-byte-key
```

Without those three there is nobody it can let in. Set them before you need them.

With [accounts](/eggy/configure/accounts/) configured there is no password and
none is read: safe mode offers **Sign in with Google** against the account list,
the sign-in client and the public base URL it can still read from the broken
file, using the existing session database. If even that cannot be established,
safe mode answers only the health probes and `config.yaml` has to be repaired on
the host.

## Common ways to land here

- an unknown YAML key — rejected on purpose, so a misspelling cannot silently
  disable a restriction;
- a model alias naming a provider that does not exist;
- a provider whose `api_key_env` variable is empty;
- Telegram configured with only one of its two secrets;
- a configured repository that cannot be cloned, or whose base branch is absent;
- a malformed `heartbeat.active_hours` window.

The first three are visible in the error text. The repository case is worth
knowing separately: it passes a config *load* and fails during construction, so
it is reachable even from a `/restart` that pre-flighted cleanly.

## Its relationship to `/restart`

`/restart` and the panel's Restart button deliberately refuse a config that will
not load, precisely so you do not fall into safe mode from a phone by accident.
Safe mode is the recovery path when something got past that — not a step in the
ordinary edit-and-apply loop. See
[Restarting to apply config](/eggy/configure/configuration/#restarting-to-apply-config).
