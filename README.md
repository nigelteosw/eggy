# Eggy

Eggy is a single-user personal agent that runs continuously on Railway and talks through Telegram. A configurable OpenAI-compatible provider handles agent reasoning; DeepSeek Pro is the default. Read-only repository questions (browsing files, searching, checking status/branches, reading GitHub issue/PR/check metadata) are answered directly, without starting a repository-modifying run. The same configured reasoning model owns editing, testing, and debugging too, using its own `read_file`/`terminal`/`patch`/`write_file` tools inside an isolated branch — there is no separate coding agent or CLI to install. A validated implementation run is committed, pushed, and opened as a pull request automatically, with no Telegram approval in between; the owner reviews the resulting pull request on GitHub. Calendar writes still require a separate Telegram approval.

Eggy is a Go ports-and-adapters modular monolith with file-backed state. It supports exactly one owner and one `eggyd` replica. This is the operator guide; see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the current internal architecture and [`docs/adr/`](docs/adr/) for durable design decisions.

## What is implemented

- Telegram webhook authentication, owner allowlisting, update deduplication, messages, and approval callbacks.
- Registered command suggestions, HTML-formatted replies with plain-text fallback, long-message splitting, typing indicators, and in-place message edits for approval outcomes and implementation-run progress.
- Named model aliases backed by configurable OpenAI-compatible providers, a bounded tool loop, persisted selection, and provider-reported usage totals.
- Atomic versioned `state.json`, layered `SOUL.md`/`USER.md`/`MEMORY.md` context, controlled agent-curated updates, and bounded conversation history.
- Exact and five-field cron schedules, quiet hours, heartbeat throttling, and weekly proactive limits.
- Restricted local workspaces, sanitized child environments, command time/output limits, and process-group cancellation.
- A native Go implementation loop (`read_file`, `terminal`, `patch`, `write_file`, `finish_implementation`) that runs the same selected model against an isolated branch checkout, with its own step budget, normalized to the same Telegram progress.
- Narrow, provider-neutral read-only repository tools (`read_file`, `terminal`, GitHub repository/issue/pull-request/check-run metadata) that never start an implementation run, create a branch, or leave a diff.
- PAT-backed Git clone/push through temporary askpass, diff/commit capture, and GitHub pull-request creation.
- Google OAuth, AES-256-GCM refresh-token storage, Calendar reads, idempotent creates, and ETag-bound writes.
- Independent, expiring, payload-digest-bound approvals that can safely resume after restart.
- `eggyd`, the companion `eggy` CLI, Docker, Railway, and a fake-adapter smoke mode.

## Local setup

Requirements: Go 1.26, Git, and Docker for the container smoke test.

```sh
brew install go
cp config.example.yaml config.yaml
cp .env.example .env
```

Edit `config.yaml`: set the public URL, numeric Telegram owner ID, provider/model aliases, repository registry, quiet hours, and Calendar defaults. For local persistence, change `data_dir` to `./data`; keep `runner.root` below that directory (for example `./data/runs`) so coding sessions survive restarts.

Provider keys are named indirectly. Each `providers.<name>.api_key_env` value identifies an environment-variable name; the secret itself must never appear in YAML or Telegram. To add another OpenAI-compatible model, add its provider and alias, then define the referenced environment variable outside the config:

```yaml
providers:
  openrouter:
    adapter: openai_compatible
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
models:
  openrouter-pro:
    provider: openrouter
    model: your-provider-model-id
```

Eggy creates three private context files in `data_dir` and never overwrites existing content:

- `SOUL.md` defines the agent's durable identity and is read-only to model tools.
- `USER.md` stores stable owner preferences and facts.
- `MEMORY.md` stores durable working knowledge selected by the agent.

The agent can append or replace named sections in `USER.md` and `MEMORY.md`. Secret-like content is rejected. Store tokens, passwords, OAuth credentials, and private keys only in the environment or the provider's credential store.

Fill `.env`. Generate the 32-byte encryption key with:

```sh
openssl rand -base64 32
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

Operational shortcuts are `/status`, `/repositories`, `/runs`, `/continue [run-id] [instruction...]`, `/stop <run-id>`, `/schedules`, `/memory`, `/clear`, `/model`, `/model <alias>`, `/model default`, `/config get <providers|models|calendar|path>`, `/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>`, `/config set model alias=<alias> provider=<provider> model=<model_id>`, `/config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]`, `/usage`, `/usage reset`, and `/restart` (applies a config change picked up on the next restart). Natural language remains the main interface.

`/continue` is owner-triggered only. With no run ID it picks the latest resumable implementation session; a named run ID resumes that exact session. Eggy preserves a compacted tool transcript and shows concise milestones in Telegram, and every resumed result is committed, pushed, and opened as a pull request automatically, the same as a fresh run.

`/status` is a deterministic local read and consumes no model tokens. `/usage` reports locally accumulated provider-returned token counts; it is useful operational telemetry, not a substitute for the provider's billing dashboard. Model aliases and credentials are configured outside Telegram.

For repository work, Eggy clones the configured base branch, creates `eggy/<run-id>`, finds root `AGENTS.md`, runs the bounded implementation loop with the selected model, captures the diff and validation, then commits, pushes, and opens a pull request in sequence with no owner tap in between. Protected branches are still denied at push time regardless of automation. Eggy never merges; the owner reviews and merges the pull request on GitHub.

## Google Calendar

Create an OAuth client in Google Cloud and add this exact redirect URI:

```text
https://YOUR_HOST/auth/google/callback
```

Set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY`, deploy, then send Eggy this owner-only Telegram command:

```text
/calendar_auth
```

Open the short-lived, single-use enrollment URL Eggy returns. The bare `/auth/google` endpoint intentionally refuses unauthenticated enrollment attempts.

Calendar reads run automatically. Eggy can list the IDs, names, access roles, and primary status of non-hidden calendars available to the authenticated user. A general Calendar question merges events from every non-hidden calendar with event-read access, not only the primary calendar. Reads can still target one calendar by ID. Calendar and event result pages are followed completely; hidden calendars and calendars that expose only free/busy information are not presented as detailed event sources.

Creates use a deterministic event ID derived from the approved idempotency key. Updates and deletes bind the approval to the event ETag; a materially changed event requires a new approval.

## Railway deployment

1. Create a Railway service from this repository.
2. Generate a public Railway domain and add a persistent volume mounted at `/data`. Keep both `data_dir: /data` and `runner.root: /data/runs`: uncommitted coding workspaces and session transcripts live there and can be explicitly resumed after a restart.
3. Set `EGGY_TELEGRAM_OWNER_ID`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, and `DEEPSEEK_API_KEY` as service variables. `EGGY_TELEGRAM_OWNER_ID` is your numeric Telegram user ID, not your `@handle`.
4. Leave `EGGY_PUBLIC_BASE_URL` unset to use `https://$RAILWAY_PUBLIC_DOMAIN`, or set it explicitly when using a custom domain.
5. For repository support on first boot, set `EGGY_REPOSITORY_URL`. `EGGY_REPOSITORY_NAME` defaults to `eggy`, `EGGY_REPOSITORY_BASE_BRANCH` defaults to `main`, and `EGGY_REPOSITORY_PROTECTED_BRANCHES` defaults to the base branch. A configured repository also requires `GITHUB_TOKEN`.
6. Keep exactly one replica while `state.json` is the operational store, then deploy and verify `/healthz` and `/readyz`.
7. On the first start, Eggy validates these values and creates `/data/config.yaml`, `SOUL.md`, `USER.md`, and `MEMORY.md` with mode `0600`. Later starts use those files without overwriting them.

Calendar is disabled in the generated first-boot configuration. Enable it deliberately in the persisted YAML and add `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY` before running `/calendar_auth`.

`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, use `/config set provider`, `/config set model`, or `/config set calendar` (or the `eggy config set` CLI equivalents) to change those sections, then restart. Other fields — branches, server URLs — still require editing the persisted YAML directly. Run `eggy config show` to inspect the full file from a checkout with `-config` pointed at it. API keys remain Railway variables and must not be copied into that file.

`EGGY_CONFIG_YAML` is not supported. Railway supplies `PORT` automatically, and Eggy validates and uses it without persisting it into `config.yaml`.

Register the Telegram webhook and complete Google OAuth after the public Railway domain is assigned. `railway.toml` configures the Docker build, liveness check, restart policy, and single replica; the volume mount and secrets are configured in Railway.

## CLI

The companion CLI reads the same files:

```sh
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggy status
./bin/eggy -config "$PWD/config.yaml" repositories
./bin/eggy -config "$PWD/config.yaml" runs
./bin/eggy -config "$PWD/config.yaml" schedules
./bin/eggy -config "$PWD/config.yaml" memory

./bin/eggy -config "$PWD/config.yaml" config get providers
./bin/eggy -config "$PWD/config.yaml" config get models
./bin/eggy -config "$PWD/config.yaml" config get calendar
./bin/eggy -config "$PWD/config.yaml" config get path
./bin/eggy -config "$PWD/config.yaml" config set provider --name=deepseek --adapter=openai_compatible --base-url=https://api.deepseek.com/v1 --api-key-env=DEEPSEEK_API_KEY
./bin/eggy -config "$PWD/config.yaml" config set model --alias=deepseek-pro --provider=deepseek --model=deepseek-chat
./bin/eggy -config "$PWD/config.yaml" config set calendar --enabled=true --default-calendar=primary --timezone=UTC
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggy config show
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

Live credential tests are intentionally outside the default suite. Verify Telegram delivery, the configured reasoning provider, a disposable repository branch/PR, and a disposable Calendar event before relying on a production deployment.

## Security boundary

Eggy is for configured trusted repositories. Workspace roots, environment allowlists, timeouts, output caps, credential redaction, temporary askpass, and process-group termination reduce accidental exposure; same-container repository code is not a strong sandbox against a malicious repository. Provider credentials never enter model prompts, state snapshots, diffs, or structured errors.
