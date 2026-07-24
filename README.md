# Eggy

Eggy is a single-user personal agent that runs continuously on Railway and talks through Telegram, with an optional embedded web chat UI as a second, independent channel (see [Web UI](#web-ui)). A configurable OpenAI-compatible provider handles agent reasoning; DeepSeek Pro is the default. Read-only repository questions (browsing files, searching, checking status/branches, reading GitHub issue/PR/check metadata) are answered directly, without starting a repository-modifying run. The same configured reasoning model owns editing, testing, and debugging too, using its own `read_file`/`terminal`/`patch`/`write_file` tools inside an isolated branch — there is no separate coding agent or CLI to install. A validated implementation run is committed, pushed, and opened as a pull request automatically, with no Telegram approval in between; the owner reviews the resulting pull request on GitHub. Calendar writes still require a separate Telegram approval.

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
- Generic remote MCP clients using the official Go SDK, with discovery, exact tool filters, namespaced tools, isolated server failures, and encrypted durable OAuth.
- Independent, expiring, payload-digest-bound approvals that can safely resume after restart.
- `eggyd`, the companion `eggy` CLI, Docker, Railway, and a fake-adapter smoke mode.
- An optional embedded web UI: session-authenticated multi-threaded chat with SSE streaming and inline approvals, plus a settings panel for providers/models/calendar/MCP — an independent channel into the same agent core as Telegram, not a mirror of it.

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

Eggy creates four private context files in `data_dir` and never overwrites existing content:

- `SOUL.md` defines the agent's durable identity and is read-only to model tools.
- `USER.md` stores stable owner preferences and facts.
- `MEMORY.md` stores durable working knowledge selected by the agent.
- `HEARTBEAT.md` is a plain checklist of what to look at on a heartbeat turn — edit it directly on disk. It has no agent tool and never carries timing, timezone, quiet-hours, limit, or prohibited-action policy; those stay fixed in config and code.

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

Operational shortcuts are `/status`, `/repositories`, `/runs`, `/continue [run-id] [instruction...]`, `/stop <run-id>`, `/schedules`, `/memory`, `/skills`, `/skills show <name>`, `/skills add <name>`, `/skills edit <name>`, `/skills remove <name>` (add/edit/remove require owner approval), `/skills disable <name>`, `/skills enable <name>`, `/clear`, `/model`, `/model <alias>`, `/model default`, `/thinking`, `/thinking show`, `/thinking hide`, `/config get <providers|models|calendar|path>`, `/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>`, `/config set model alias=<alias> provider=<provider> model=<model_id>`, `/config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]`, `/mcp`, `/mcp status <server>`, `/mcp probe <server>`, `/mcp login <server>`, `/mcp logout <server>`, `/mcp reload <server>`, `/usage`, `/usage reset`, and `/restart` (applies a config change picked up on the next restart). Natural language remains the main interface.

Procedural skills are Markdown files under `skills/` in `data_dir`, each with a `name`/`description` frontmatter pair and a body of instructions (see [`docs/adr/0005-procedural-skills.md`](docs/adr/0005-procedural-skills.md)). Only the compact `name: description` index stays in context every turn; the agent loads one skill's full body with `skill_read` when its description matches the current task. Creating, editing, or deleting a skill always requires owner approval, the same digest-bound flow as Calendar mutations and adding a repository — a skill's body is instructions that steer later tool calls, not a stored fact. Disabling or re-enabling a skill takes effect immediately with no approval, since it only changes what is surfaced, never a skill's content.

`/continue` is owner-triggered only. With no run ID it picks the latest resumable implementation session; a named run ID resumes that exact session. Eggy preserves a compacted tool transcript and shows concise milestones in Telegram, and every resumed result is committed, pushed, and opened as a pull request automatically, the same as a fresh run.

`/status` is a deterministic local read and consumes no model tokens. `/usage` reports locally accumulated provider-returned token counts; it is useful operational telemetry, not a substitute for the provider's billing dashboard. Model aliases and credentials are configured outside Telegram.

For repository work, Eggy clones the configured base branch, creates `eggy/<run-id>`, finds root `AGENTS.md`, runs the bounded implementation loop with the selected model, captures the diff and validation, then commits, pushes, and opens a pull request in sequence with no owner tap in between. Protected branches are still denied at push time regardless of automation. Eggy never merges; the owner reviews and merges the pull request on GitHub.

## Web UI

Eggy also ships an embedded web UI — a React/TypeScript/Tailwind single-page app, built by `make build-web` and served directly by `eggyd` itself (no separate hosting, no separate process). It's optional and off by default: set `EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD`, and `EGGY_ENCRYPTION_KEY` to enable it, then it's reachable at Eggy's public URL.

Telegram and the web UI are independent channels into the same agent core — the same dispatcher, tool loop, and approval engine — not mirrors of one shared conversation. A message sent on one never appears on, or affects, the other. Telegram keeps writing to its own single, fixed, continuous thread exactly as described above. The web UI instead gives you a sidebar of multiple, independently-resumable conversation threads: switch between them, start a new one, and whatever the model does inside a thread — general chat, a coding run, a calendar action — is just tool calls within that thread's turn, the same as it already works for Telegram. New threads are auto-titled from their first message.

Authentication is a single owner login (the `EGGY_UI_USER_EMAIL`/`EGGY_UI_PASSWORD` pair, submitted once at `/api/login`), backed by a signed session cookie (`EGGY_ENCRYPTION_KEY`, 12-hour TTL) rather than a per-request credential; repeated failed logins from the same address are throttled with an increasing delay. There is no per-user account system — Eggy is still single-owner, the web UI is just a second door to the same owner.

Besides chat, the web UI has a settings panel that mirrors the same `/config` surface available via Telegram/CLI — providers, model aliases, the Calendar toggle, and MCP server definitions — and renders the same inline approve/reject buttons Telegram's callback buttons trigger, for an approval requested during a web-chat turn.

The route surface: `POST /api/login`, `POST /api/logout`, and `GET /api/session` for auth; `GET /api/chat/threads` and `POST /api/chat/threads` to list/create threads; `GET .../history`, `GET .../stream` (SSE), and `POST .../send` under `/api/chat/threads/{id}/`; `POST /api/chat/approve` for approval decisions (shape-compatible with Telegram's callback flow); and `GET`/`POST` `/api/config/{providers,models,calendar,mcp}` plus `DELETE /api/config/mcp/{name}` for the settings panel. Everything except login is behind the session cookie.

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

## MCP servers and Railway

Remote Streamable HTTP MCP servers are configured under `mcp.servers`. The supplied example enables Railway's hosted server at `https://mcp.railway.com` with OAuth and an explicit curated tool list. `list-variables` is deliberately excluded because its results can place deployment secrets directly into model context; this is a Railway filter choice, not a hardcoded adapter rule.

Set `EGGY_ENCRYPTION_KEY`, deploy or restart Eggy, then authorize Railway from the owner-only command surface:

```text
/mcp login railway
/mcp probe railway
/mcp status railway
```

Open the returned Railway authorization URL and approve the intended workspace and projects. The callback returns to `https://YOUR_HOST/auth/mcp/railway/callback`; Eggy stores the dynamic client information and tokens encrypted under `/data/mcp/railway/oauth.json`, requests a controlled restart, and discovers the filtered tool catalog. A successful probe should show tools such as `railway__list_projects` and `railway__get_logs`. `/mcp logout railway` removes only Railway's OAuth record, while `/mcp reload railway` restarts Eggy to rediscover a changed catalog.

MCP tools are available only on direct owner turns. Scheduled turns, heartbeat turns, and repository implementation runs never receive them. One unavailable or unauthenticated server is non-fatal to readiness and does not hide another ready server. Tool calls have configured time and output limits; binary content is reduced to metadata rather than copied into model context.

Additional remote servers use the same adapter. Add another named entry beneath `mcp.servers`, choose `auth: oauth`, `auth: bearer-env` with `bearer_token_env: SOME_ENV_NAME`, or `auth: none`, and set exact `tool_filter.include`/`exclude` names. Version 1 supports Streamable HTTP tools only—not stdio, legacy SSE, resources, prompts, roots, sampling, elicitation, or MCP Apps.

## Railway deployment

1. Create a Railway service from this repository.
2. Generate a public Railway domain and add a persistent volume mounted at `/data`. Keep both `data_dir: /data` and `runner.root: /data/runs`: uncommitted coding workspaces and session transcripts live there and can be explicitly resumed after a restart.
3. Set `EGGY_TELEGRAM_OWNER_ID`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, and `DEEPSEEK_API_KEY` as service variables. `EGGY_TELEGRAM_OWNER_ID` is your numeric Telegram user ID, not your `@handle`.
4. Leave `EGGY_PUBLIC_BASE_URL` unset to use `https://$RAILWAY_PUBLIC_DOMAIN`, or set it explicitly when using a custom domain.
5. For repository support on first boot, set `EGGY_REPOSITORY_URL`. `EGGY_REPOSITORY_NAME` defaults to `eggy`, `EGGY_REPOSITORY_BASE_BRANCH` defaults to `main`, and `EGGY_REPOSITORY_PROTECTED_BRANCHES` defaults to the base branch. A configured repository also requires `GITHUB_TOKEN`.
6. Keep exactly one replica while `state.json` is the operational store, then deploy and verify `/healthz` and `/readyz`.
7. On the first start, Eggy validates these values and creates `/data/config.yaml`, `SOUL.md`, `USER.md`, `MEMORY.md`, and `HEARTBEAT.md` with mode `0600`. Later starts use those files without overwriting them.

Calendar is disabled in the generated first-boot configuration. Enable it deliberately in the persisted YAML and add `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY` before running `/calendar_auth`.

For Railway MCP, keep the `mcp.servers.railway` block from `config.example.yaml`, set `EGGY_ENCRYPTION_KEY`, restart, and run `/mcp login railway`. Existing persisted configs are not rewritten automatically; add the block deliberately. No Railway API token is needed in OAuth mode.

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

./bin/eggy -config "$PWD/config.yaml" mcp
./bin/eggy -config "$PWD/config.yaml" mcp status railway
./bin/eggy -config "$PWD/config.yaml" mcp probe railway
./bin/eggy -config "$PWD/config.yaml" mcp login railway
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

Live credential tests are intentionally outside the default suite. Verify Telegram delivery, the configured reasoning provider, a disposable repository branch/PR, a disposable Calendar event, and Railway MCP login/probe plus a bounded `railway__list_projects` or `railway__get_logs` call before relying on a production deployment. Do not use `list-variables` for that check.

## Security boundary

Eggy is for configured trusted repositories. Workspace roots, environment allowlists, timeouts, output caps, credential redaction, temporary askpass, and process-group termination reduce accidental exposure; same-container repository code is not a strong sandbox against a malicious repository. Provider credentials never enter model prompts, state snapshots, diffs, or structured errors.
