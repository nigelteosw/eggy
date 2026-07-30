---
title: Health checks
description: Use Eggy's liveness, readiness, logs, and provider checks to monitor a running instance.
eyebrow: Operate
---

Eggy exposes two unauthenticated operational endpoints.

## Liveness

```sh
curl https://your-eggy.example/healthz
```

A healthy HTTP process returns:

```text
ok
```

Railway uses this route as the deployment healthcheck.

## Readiness

```sh
curl https://your-eggy.example/readyz
```

This route returns `ready` only when the application's readiness check passes. Otherwise it returns HTTP `503` with `not ready`.

## What health does not prove

- It does not make a model completion.
- It does not send a Telegram reply.
- It does not prove Calendar OAuth is connected.
- It does not call every enabled MCP tool.

Use an owner conversation for end-to-end channel and model verification. Use the relevant OAuth or MCP flow for integrations.

## Logs

Eggy writes `gateway.log` and `errors.log` under the home `logs/` directory and emits structured logs to the deployment environment. Loaded secret values are redacted before logging.
