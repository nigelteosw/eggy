# Eggy

Eggy is a single-user personal agent that runs continuously on Railway and talks through Telegram. DeepSeek handles ordinary assistant work; Codex owns repository inspection, editing, testing, and debugging. Commit, push, pull-request creation, and Calendar writes each require a separate Telegram approval.

The MVP is a Go ports-and-adapters modular monolith with file-backed state. It supports exactly one owner and one `eggyd` replica.

## What is implemented

- Telegram webhook authentication, owner allowlisting, update deduplication, messages, and approval callbacks.
- DeepSeek Flash/Pro routing with a bounded tool loop and one-time deterministic escalation.
- Atomic versioned `state.json`, controlled `MEMORY.md` updates, and bounded conversation history.
- Exact and five-field cron schedules, quiet hours, heartbeat throttling, and weekly proactive limits.
- Restricted local workspaces, sanitized child environments, command time/output limits, and process-group cancellation.
- Codex `exec --json` execution with normalized Telegram progress.
- PAT-backed Git clone/push through temporary askpass, diff/commit capture, and GitHub pull-request creation.
- Google OAuth, AES-256-GCM refresh-token storage, Calendar reads, idempotent creates, and ETag-bound writes.
- Independent, expiring, payload-digest-bound approvals that can safely resume after restart.
- `eggyd`, the companion `eggy` CLI, Docker, Railway, and a fake-adapter smoke mode.

## Local setup

Requirements: Go 1.26, Git, Codex CLI, and Docker for the container smoke test.

```sh
brew install go
cp config.example.yaml config.yaml
cp .env.example .env
```

Edit `config.yaml`: set the public URL, Telegram owner ID, repository registry, quiet hours, and Calendar defaults. For local persistence, change `data_dir` to `./data`. Keep runner workspaces below `/tmp/runs` or another dedicated root.

Fill `.env`. Generate the 32-byte encryption key with:

```sh
openssl rand -base64 32
```

Install and authorize Codex locally:

```sh
npm install -g @openai/codex
export CODEX_HOME="$PWD/data/codex"
codex login --device-auth
```

Run and verify:

```sh
make test
make race
make build
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggyd
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

The local `.env` file is loaded automatically. Process environment values take precedence over `.env` values.

## Telegram

Create a bot with BotFather, obtain your numeric Telegram user ID, and set both in configuration/secrets. Register the webhook after `eggyd` is publicly reachable:

```sh
curl --fail --request POST \
  "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/setWebhook" \
  --header 'Content-Type: application/json' \
  --data "{\"url\":\"https://YOUR_HOST/webhooks/telegram\",\"secret_token\":\"${TELEGRAM_WEBHOOK_SECRET}\",\"allowed_updates\":[\"message\",\"callback_query\"]}"
```

Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, and `/new`. Natural language remains the main interface.

For repository work, Eggy clones the configured base branch, creates `eggy/<run-id>`, finds root `AGENTS.md`, runs Codex, captures the diff and validation, and then requests commit approval. A successful commit causes a separate push approval; a successful push causes a separate pull-request approval. Protected branches are denied regardless of approval. Eggy never merges.

## Google Calendar

Create an OAuth client in Google Cloud and add this exact redirect URI:

```text
https://YOUR_HOST/auth/google/callback
```

Set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY`, deploy, then send Eggy this owner-only Telegram command:

```text
/calendar-auth
```

Open the short-lived, single-use enrollment URL Eggy returns. The bare `/auth/google` endpoint intentionally refuses unauthenticated enrollment attempts.

Calendar reads run automatically. Creates use a deterministic event ID derived from the approved idempotency key. Updates and deletes bind the approval to the event ETag; a materially changed event requires a new approval.

## Railway deployment

1. Create a Railway service from this repository.
2. Add a persistent volume mounted at `/data`.
3. Upload or create `/data/config.yaml` from `config.example.yaml`.
4. Add every value from `.env.example` as a Railway service variable.
5. Keep exactly one replica while `state.json` is the operational store.
6. Deploy and verify `/healthz` and `/readyz`.
7. Open a shell in the running service and authorize the persisted Codex home:

```sh
export CODEX_HOME=/data/codex
codex login --device-auth
```

Complete the device authorization in a browser. The Railway Volume preserves Codex-managed authentication across container replacement. Do not copy Codex credentials into `.env` or `state.json`.

The image pins Codex CLI `0.144.5`; override the `CODEX_VERSION` build argument deliberately when upgrading and rerun the complete verification suite.

Register the Telegram webhook and complete Google OAuth after the public Railway domain is assigned. `railway.toml` configures the Docker build, liveness check, restart policy, and single replica; the volume mount and secrets are configured in Railway.

## CLI

The companion CLI reads the same files and is deliberately structured as a future TUI host:

```sh
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggy status
./bin/eggy -config "$PWD/config.yaml" repositories
./bin/eggy -config "$PWD/config.yaml" runs
./bin/eggy -config "$PWD/config.yaml" schedules
./bin/eggy -config "$PWD/config.yaml" memory
./bin/eggy -config "$PWD/config.yaml" new
```

## Verification

```sh
make fmt
make vet
make test
make race
make build
make smoke
```

`make smoke` builds the production image, starts `eggyd` with fake external adapters and a temporary `/data` volume, checks readiness and liveness from inside the container, and removes the container and temporary data.

Live credential tests are intentionally outside the default suite. Verify Telegram delivery, DeepSeek responses, a disposable repository branch/PR, and a disposable Calendar event before relying on a production deployment.

## Security boundary

Eggy is for configured trusted repositories. Workspace roots, environment allowlists, timeouts, output caps, credential redaction, temporary askpass, and process-group termination reduce accidental exposure; same-container repository code is not a strong sandbox against a malicious repository. Provider credentials never enter model prompts, state snapshots, diffs, or structured errors.
