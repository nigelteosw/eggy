---
title: Deploy on Railway
description: Run one durable Eggy daemon on Railway with a mounted home directory and platform health checks.
eyebrow: Get started
---

Eggy's container and `railway.toml` are designed for a single Railway replica. Durable state belongs on a Railway volume mounted at `/data`.

## Create the service

Connect the GitHub repository to a Railway service. Railway builds the root `Dockerfile`, starts `eggyd`, and checks `GET /healthz`.

Keep the deployment at one replica. Local file locks protect stores within the shared home, but Eggy is not designed as a horizontally scaled service.

## Mount persistent storage

Create a Railway volume and mount it at:

```text
/data
```

The image does not declare a Docker `VOLUME`; Railway owns the mount. Losing this volume loses configuration, OAuth records, memory, schedules, skills, logs, and repository workspaces.

## First-boot variables

When `/data/config.yaml` does not exist, Eggy generates it from environment variables. Set:

| Variable | Purpose |
| --- | --- |
| `EGGY_TELEGRAM_OWNER_ID` | Numeric Telegram owner; also becomes the canonical owner ID |
| `EGGY_OWNER_ID` | Canonical owner for a web-only deployment |
| `EGGY_PUBLIC_BASE_URL` | Public `https://` URL when Railway does not inject a domain |
| `DEEPSEEK_API_KEY` | Default generated provider credential |
| `EGGY_REPOSITORY_URL` | Optional first configured repository |
| `EGGY_REPOSITORY_NAME` | Optional repository name; defaults to `eggy` |
| `EGGY_REPOSITORY_BASE_BRANCH` | Optional base branch; defaults to `main` |
| `EGGY_REPOSITORY_PROTECTED_BRANCHES` | Optional comma-separated list |

Railway's `RAILWAY_PUBLIC_DOMAIN` is used automatically when `EGGY_PUBLIC_BASE_URL` is absent. Railway's injected `PORT` overrides `server.listen`.

## Surface credentials

For Telegram:

```text
TELEGRAM_BOT_TOKEN
TELEGRAM_WEBHOOK_SECRET
```

For authenticated web chat:

```text
EGGY_UI_USER_EMAIL
EGGY_UI_PASSWORD
EGGY_ENCRYPTION_KEY
```

`EGGY_ENCRYPTION_KEY` is also required by OAuth-backed MCP servers. Use a base64-encoded 32-byte key and keep it stable; changing it makes existing encrypted credentials unreadable.

## Verify deployment

Check `/healthz`, then `/readyz`. A Telegram webhook returning `204` means the update entered Eggy's queue; it does not prove that the asynchronous model turn or outbound reply completed. Use Railway logs and a real owner message for end-to-end verification.
