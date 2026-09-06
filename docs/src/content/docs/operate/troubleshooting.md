---
title: Troubleshooting
description: Diagnose common startup, channel, OAuth, MCP, repository, and build failures from the concrete failing path.
eyebrow: Operate
---

Start with the exact command, URL, startup log, or runtime path that failed. Avoid treating a successful healthcheck as proof of a complete integration.

## Eggy will not start

On a deployment, it does not fail silently: a startup failure serves the
[safe-mode](/eggy/operate/safe-mode/) repair page, `/readyz` reports the error,
and the authenticated owner can fix `config.yaml` in the browser. Locally, run the
daemon in the foreground and read the first configuration error. Common causes
are:

- an unknown YAML field;
- a model alias referencing a missing provider;
- a missing provider key named by `api_key_env`;
- Telegram configured without both Telegram secrets;
- web credentials set partially or without `EGGY_ENCRYPTION_KEY`;
- a configured repository that cannot be cloned or whose base branch is absent.

## Telegram returns 204 but no reply arrives

`204` proves webhook acceptance only. Check, in order:

1. `/healthz` and `/readyz`;
2. Telegram's registered webhook URL and secret;
3. the numeric owner ID;
4. `EGGY_FAKE_ADAPTERS` is not `1`;
5. model-provider credentials and completion logs;
6. outbound Telegram API errors.

## MCP tools are absent

Check `enabled`, transport-specific fields, authentication, exact tool filters, connection timeout, and logs for discovery errors. A quarantined tool returns after its cooldown; one failing server does not disable native tools.

## Repository tools fail

Confirm `GITHUB_TOKEN`, clone URL, base branch, and repository name. Requested paths must stay within the opened workspace. Repository mutation is intentionally unavailable.

## A turn fails with "turn input exceeds the context budget"

What a turn may not compact away — instructions, durable context, the request and
any steering, plus the newest step — does not fit the model budget on its own.
Almost always this is a `MEMORY.md` or `USER.md` that has grown without pruning,
or a single enormous tool result. Trim the document, or narrow the call. The
[trace](/eggy/use/traces/) for the failed turn shows which part is large.

## A skill never gets used

Check, in order:

1. the file is in `skills/` in the Eggy home, ends in `.md`, and its `name`
   frontmatter is 1–64 lowercase letters, digits, or hyphens;
2. the logs for a warning naming it — an oversized (over 32 KB), unreadable, or
   malformed file is skipped rather than failing the listing, and so is any skill
   beyond the 16 KB index cap;
3. the `description`, which is the only thing the model sees before deciding to
   load the skill. Write it as *when to use this*, not as a title.

A [trace](/eggy/use/traces/) settles it: the model call's prompt contains the
Available skills index exactly as it was sent.

## The Traces view is empty or missing

Missing entirely means `tracing.enabled: false` — the routes are not mounted, so
the panel says the capability is off rather than showing an empty list. Empty
means the traces were pruned: `keep_turns` bounds how many are retained
regardless of age and `retention` drops older ones, with pruning running as each
turn completes.

## The heartbeat never says anything

That is usually correct behavior — a beat delivers only when there is something
worth saying. Check in order: an interval is set; `memories/WATCH.md` is not empty
(an empty list skips the beat with no model call, and warns once at startup); a
`telegram` block exists, since there is nowhere else to deliver unprompted output;
and `active_hours`, if set, is not excluding the times you are looking. The logs
record each beat's requested and actual next wake.

## Local Go tests cannot write their cache

In a restricted environment, point only the build cache at a writable temporary path:

```sh
GOCACHE=/tmp/eggy-go-cache make test
```

## Docker smoke cannot run

`make smoke` requires a reachable Docker daemon. A missing socket or stopped daemon is an environment blocker, not a passing smoke test.
