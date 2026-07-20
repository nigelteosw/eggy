# Eggy

Eggy is a single-user personal agent that runs continuously on Railway and talks through Telegram. A configurable OpenAI-compatible provider handles agent reasoning; DeepSeek Pro is the default. Read-only repository questions (browsing files, searching, checking status/branches, reading GitHub issue/PR/check metadata) are answered directly, without launching a coding agent. A configurable coding agent — Codex CLI or Claude Code, selectable with `/coding_agent` — owns editing, testing, and debugging. Commit, push, pull-request creation, and Calendar writes each require a separate Telegram approval.

The MVP is a Go ports-and-adapters modular monolith with file-backed state. It supports exactly one owner and one `eggyd` replica.

## What is implemented

- Telegram webhook authentication, owner allowlisting, update deduplication, messages, and approval callbacks.
- Registered command suggestions, HTML-formatted replies with plain-text fallback, long-message splitting, typing indicators, and in-place message edits for approval outcomes and coding-agent run progress.
- Named model aliases backed by configurable OpenAI-compatible providers, a bounded tool loop, persisted selection, and provider-reported usage totals.
- Atomic versioned `state.json`, layered `SOUL.md`/`USER.md`/`MEMORY.md` context, controlled agent-curated updates, and bounded conversation history.
- Exact and five-field cron schedules, quiet hours, heartbeat throttling, and weekly proactive limits.
- Restricted local workspaces, sanitized child environments, command time/output limits, and process-group cancellation.
- Configurable coding-agent runtime: Codex `exec --json` or Claude Code `-p --output-format stream-json`, persisted selection, both normalized to the same Telegram progress.
- Narrow, provider-neutral read-only repository tools (bounded directory tree, file/text search, bounded file reads, git status/branches, GitHub repository/issue/pull-request/check-run metadata) that never launch a coding agent, create a branch, or leave a diff.
- PAT-backed Git clone/push through temporary askpass, diff/commit capture, and GitHub pull-request creation.
- Google OAuth, AES-256-GCM refresh-token storage, Calendar reads, idempotent creates, and ETag-bound writes.
- Independent, expiring, payload-digest-bound approvals that can safely resume after restart.
- `eggyd`, the companion `eggy` CLI, Docker, Railway, and a fake-adapter smoke mode.

## Local setup

Requirements: Go 1.26, Git, Codex CLI and/or Claude Code CLI, and Docker for the container smoke test.

```sh
brew install go
cp config.example.yaml config.yaml
cp .env.example .env
```

Edit `config.yaml`: set the public URL, numeric Telegram owner ID, provider/model aliases, repository registry, quiet hours, and Calendar defaults. For local persistence, change `data_dir` to `./data`. Keep runner workspaces below `/tmp/runs` or another dedicated root.

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

Install and authorize Codex locally:

```sh
npm install -g @openai/codex
export CODEX_HOME="$PWD/data/codex"
codex login --device-auth
```

Claude Code is optional; configure it in `coding.agents` alongside or instead of Codex. Install it and generate a long-lived token with `claude setup-token`, then set `CLAUDE_CODE_OAUTH_TOKEN` (never in YAML) and point `CLAUDE_CONFIG_DIR` at persisted storage:

```sh
npm install -g @anthropic-ai/claude-code
export CLAUDE_CONFIG_DIR="$PWD/data/claude"
claude setup-token
```

`claude setup-token` prints a token that is valid for one year; export it as `CLAUDE_CODE_OAUTH_TOKEN` and renew it before expiry. Switch the active coding agent at runtime with the owner-only Telegram command `/coding_agent <alias>` (or `/coding_agent default` to restore `coding.default_agent`).

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

Operational shortcuts are `/status`, `/repositories`, `/runs`, `/stop <run-id>`, `/schedules`, `/memory`, `/new`, `/model`, `/model <alias>`, `/model default`, `/coding_agent`, `/coding_agent <alias>`, `/coding_agent default`, `/config get <coding|providers|models|calendar|path>`, `/config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]`, `/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>`, `/config set model alias=<alias> provider=<provider> model=<model_id>`, `/config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]`, `/usage`, and `/usage reset`. Natural language remains the main interface.

`/status` is a deterministic local read and consumes no model tokens. `/usage` reports locally accumulated provider-returned token counts; it is useful operational telemetry, not a substitute for the provider's billing dashboard. Model aliases and credentials are configured outside Telegram.

For repository work, Eggy clones the configured base branch, creates `eggy/<run-id>`, finds root `AGENTS.md`, runs the selected coding agent, captures the diff and validation, and then requests commit approval. A successful commit causes a separate push approval; a successful push causes a separate pull-request approval. Protected branches are denied regardless of approval. Eggy never merges.

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
2. Generate a public Railway domain and add a persistent volume mounted at `/data`.
3. Set `EGGY_TELEGRAM_OWNER_ID`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, and `DEEPSEEK_API_KEY` as service variables. `EGGY_TELEGRAM_OWNER_ID` is your numeric Telegram user ID, not your `@handle`.
4. Leave `EGGY_PUBLIC_BASE_URL` unset to use `https://$RAILWAY_PUBLIC_DOMAIN`, or set it explicitly when using a custom domain.
5. For coding support on first boot, set `EGGY_REPOSITORY_URL`. `EGGY_REPOSITORY_NAME` defaults to `eggy`, `EGGY_REPOSITORY_BASE_BRANCH` defaults to `main`, and `EGGY_REPOSITORY_PROTECTED_BRANCHES` defaults to the base branch. A configured repository also requires `GITHUB_TOKEN`.
6. Keep exactly one replica while `state.json` is the operational store, then deploy and verify `/healthz` and `/readyz`.
7. On the first start, Eggy validates these values and creates `/data/config.yaml`, `SOUL.md`, `USER.md`, and `MEMORY.md` with mode `0600`. Later starts use those files without overwriting them.
8. Open a shell in the running service and authorize the persisted Codex home:

```sh
export CODEX_HOME=/data/codex
codex login --device-auth
```

Complete the device authorization in a browser. The Railway Volume preserves Codex-managed authentication across container replacement. Do not copy Codex credentials into `.env` or `state.json`.

To enable Claude Code instead of, or alongside, Codex, generate a token locally with `claude setup-token` and set `CLAUDE_CODE_OAUTH_TOKEN` as a Railway service variable — never in `config.yaml`. Then register the alias with the owner-only Telegram command `/config set coding_agent alias=claude adapter=claude_cli credential_env=CLAUDE_CODE_OAUTH_TOKEN`, or from the CLI with `eggy config set coding-agent --alias=claude --adapter=claude_cli --credential-env=CLAUDE_CODE_OAUTH_TOKEN` (pointed at the same `config.yaml` via `-config`) — no SSH session required. Restart the service for the new alias to take effect. The token is valid for one year; renew it before expiry by running `claude setup-token` again and updating the Railway variable. Set `coding.default_agent` to `claude` to make it the default, or switch at runtime with `/coding_agent claude`.

Calendar is disabled in the generated first-boot configuration. Enable it deliberately in the persisted YAML and add `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY` before running `/calendar_auth`.

`EGGY_PUBLIC_BASE_URL` and the `EGGY_REPOSITORY_*` variables are first-boot inputs. After `/data/config.yaml` exists, use `/config set coding_agent`, `/config set provider`, `/config set model`, or `/config set calendar` (or the `eggy config set` CLI equivalents) to change those sections, then restart. Other fields — branches, server URLs — still require editing the persisted YAML directly. Run `eggy config show` to inspect the full file from a checkout with `-config` pointed at it. API keys remain Railway variables and must not be copied into that file.

`EGGY_CONFIG_YAML` is not supported. Railway supplies `PORT` automatically, and Eggy validates and uses it without persisting it into `config.yaml`.

The image pins Codex CLI `0.144.5` and Claude Code `2.1.215`; override the `CODEX_VERSION` or `CLAUDE_CODE_VERSION` build argument deliberately when upgrading either, and rerun the complete verification suite.

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

Live credential tests are intentionally outside the default suite. Verify Telegram delivery, the configured reasoning provider, a disposable repository branch/PR, and a disposable Calendar event before relying on a production deployment.

## Security boundary

Eggy is for configured trusted repositories. Workspace roots, environment allowlists, timeouts, output caps, credential redaction, temporary askpass, and process-group termination reduce accidental exposure; same-container repository code is not a strong sandbox against a malicious repository. Provider credentials never enter model prompts, state snapshots, diffs, or structured errors.
