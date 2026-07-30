---
title: Troubleshooting
description: Diagnose common startup, channel, OAuth, MCP, repository, and build failures from the concrete failing path.
eyebrow: Operate
---

Start with the exact command, URL, startup log, or runtime path that failed. Avoid treating a successful healthcheck as proof of a complete integration.

## Eggy will not start

Run the daemon in the foreground and read the first configuration error. Common causes are:

- an unknown YAML field;
- a model alias referencing a missing provider;
- a missing provider key named by `api_key_env`;
- Telegram configured without both Telegram secrets;
- web credentials set partially or without `EGGY_ENCRYPTION_KEY`;
- Calendar enabled without Google credentials;
- a configured repository that cannot be cloned or whose base branch is absent.

## Telegram returns 204 but no reply arrives

`204` proves webhook acceptance only. Check, in order:

1. `/healthz` and `/readyz`;
2. Telegram's registered webhook URL and secret;
3. the numeric owner ID;
4. `EGGY_FAKE_ADAPTERS` is not `1`;
5. model-provider credentials and completion logs;
6. outbound Telegram API errors.

## Calendar tools are absent

Confirm `calendar.default_calendar` is non-empty, all three Calendar secrets are set, the daemon restarted, and `/auth/google` completed successfully.

## MCP tools are absent

Check `enabled`, transport-specific fields, authentication, exact tool filters, connection timeout, and logs for discovery errors. A quarantined tool returns after its cooldown; one failing server does not disable native tools.

## Repository tools fail

Confirm `GITHUB_TOKEN`, clone URL, base branch, and repository name. Requested paths must stay within the opened workspace. Repository mutation is intentionally unavailable.

## Local Go tests cannot write their cache

In a restricted environment, point only the build cache at a writable temporary path:

```sh
GOCACHE=/tmp/eggy-go-cache make test
```

## Docker smoke cannot run

`make smoke` requires a reachable Docker daemon. A missing socket or stopped daemon is an environment blocker, not a passing smoke test.
