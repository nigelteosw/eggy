---
title: Quickstart
description: Build Eggy, create a local configuration, and start your first web or Telegram conversation.
eyebrow: Get started
---

This guide runs Eggy directly from a checkout. It uses the same daemon and persistent home layout as a hosted deployment.

## Prerequisites

- Go 1.26
- Bun
- Git
- An API key for at least one model provider
- Optional: a Telegram bot token and webhook secret

## Build

```sh
git clone https://github.com/nigelteosw/eggy.git
cd eggy
make build
```

`make build` compiles the web application under `website/`, embeds its output in the Go binary, and writes `bin/eggyd`.

## Create configuration

```sh
cp config.example.yaml config.yaml
cp .env.example .env
```

Edit the copied files. At minimum, define one provider, one model alias, and the matching provider API key. For web login, set `EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD`, and a base64-encoded 32-byte `EGGY_ENCRYPTION_KEY`.

For Telegram, set:

```dotenv
TELEGRAM_BOT_TOKEN=...
TELEGRAM_WEBHOOK_SECRET=...
```

and keep the numeric `telegram.owner_id` in YAML.

## Start Eggy

```sh
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggyd
```

Alternatively, use explicit paths:

```sh
./bin/eggyd --home "$PWD/data" --config "$PWD/config.yaml"
```

The daemon creates its owned directories, opens durable stores, verifies configured repositories, connects enabled adapters, and starts the HTTP server.

## Check the instance

```sh
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

`healthz` proves the HTTP process is alive. `readyz` also checks adapter readiness.

Open `http://localhost:8080/` for web chat when web credentials are configured. MCP OAuth callbacks use `/auth/mcp/{server}/callback`.
