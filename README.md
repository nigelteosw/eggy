# Eggy

Eggy is a personal agent written in Go for a small group of people. One
daemon serves Telegram, Discord DMs, and an authenticated web panel, routes
requests to configured model providers, keeps conversation and durable memory,
and exposes a small tool registry. Each person signs in to their own account
and owns their conversations, memory, schedules, approvals, and settings; the
configuration and integrations are shared.

Repository inspection is read-only: Eggy can clone configured repositories,
read files and GitHub metadata, and keep a checkout attached to a conversation.
The one write path is the optional agent runner extension, which edits a
sandboxed checkout and may push one `eggy/<run-id>` branch and open one pull
request, and only from a run a person started in the panel or with `/code run`.
Eggy never merges, approves, tags, force-pushes, or touches a protected branch.

## Surfaces

Telegram and Discord are for conversation and notices. Telegram also carries
approval buttons and inline choices; Discord has no decision surface, so an
approval raised there points to the web panel. Telegram's `/` menu offers the
everyday commands (`/menu`, `/stop`, `/clear`, `/web`, `/settings`, `/help`),
and these work when typed:

- `/status`, `/soul`, `/restart`
- `/model`, `/mode [strict|normal|auto]`, `/heartbeat` — personal settings
- `/mcp`, `/google` — shared administration
- `/code` — when the agent runner extension is enabled

`/help <topic>` shows the syntax. The agent can call `telegram_select` with
2–8 labelled options; a tap returns the value as the person's next message,
expires after ten minutes, and never authorizes a protected action. No secret
is accepted as a chat argument: MCP settings name environment variables whose
values must already exist in the deployment.

The web panel has Home (what needs you, what Eggy is watching, what is coming
up), Chat, Scheduled, Customize (skills, MCP and Google connectors, and
marketplace plugins when enabled), and Traces, which shows each turn as it ran.
Settings opens over any of them at `/settings`, and enabled extensions such as
Finance add their own entry to the rail.

Every surface is a view onto one administration authority: each config write
goes through `internal/config` under the same file lock with the same
validation. `/restart` and the panel's Restart share one path that checks the
new config loads, lets in-flight turns finish, and rebuilds the daemon in the
same process, so config edited from a phone is applied from a phone.

## Turns

A turn ends when the model stops calling tools, not when it has done a fixed
amount of work. A message sent while a turn is running steers it at the next
step boundary rather than queueing behind it; a steer that arrives too late to
be read is delivered as its own turn rather than dropped. When the exchange the
loop produced outgrows its context budget, the oldest steps fold into a running
checkpoint and the turn continues — instructions, durable context, the request,
and every steer are never compacted away. `/stop` cancels at the same step
boundary.

## Skills

`skills/` holds one procedure per Markdown file or directory, each with `name`
and `description` frontmatter. Only the summaries are resident in the prompt;
the model loads a full body, or one of a directory skill's reference files, by
exact name with `skill_read`. Skills are added by placing the file, writing one
in Customize, or installing from a marketplace after reviewing exactly what it
writes; a skill grants no tool and lifts no approval. A file that is oversized, unreadable, or malformed is
skipped with a warning naming it, and the aggregate index is capped, so one bad
file cannot break the list.

## Repository inspection

Repositories are declared under `repositories` in `config.yaml`. Eggy verifies
that each configured remote and base branch is readable during startup, then
synchronizes the active set from the file. Adding or removing one is a config
edit plus restart.

Available repository tools are read-only:

- `repository_list`
- `repository_github`
- `workspace_open`
- `read_file`
- `workspace_close`

Configured GitHub credentials remain inside the adapter. There is no
agent-callable terminal through which they can leak.

## Web reach

Set `tavily.enabled` in `config.yaml` with a Tavily API key and Eggy gains two
read-only tools:

- `web_search` finds pages and returns ranked snippets;
- `web_extract` reads up to five of those pages as markdown.

Search returns snippets rather than pages, so reading a result is a second
call. Responses are bounded by `tavily.max_output_bytes`, split evenly across
results so one long page cannot starve the others.

Left out of `config.yaml`, the capability does not exist: no client is built,
no tool is registered, and neither schema reaches a model request. See
`docs/src/content/docs/configure/web-search.md`.

## Code map

Eggy is ports and adapters, grouped by capability family: `internal/core`
speaks only to interfaces in `internal/ports`, providers under
`internal/<family>/` implement them, and
`internal/bootstrap` is the one place that wires them together. Every package
opens with a comment saying what it owns and, for the larger ones, which file
holds what (`go doc ./internal/panel`).

Entry point and wiring

- `cmd/eggyd` — the daemon: startup supervision, first-run setup, safe mode, restart.
- `internal/bootstrap` — builds adapters from config, registers tools, runs the event loop and heartbeat.
- `internal/home` — every path in the home directory.

Core (provider-neutral)

- `internal/ports` — the interfaces adapters implement, one file per topic.
- `internal/core/agent` — the model-and-tools loop, prompt building, context compaction.
- `internal/core/turns` — what one turn is: owner, scheduled, or heartbeat, and what each may reach.
- `internal/core/services` — dispatcher, tool registry, approval gate and service, conversation, native tools, traces.
- `internal/core/services/repo` — read-only repository and workspace tools.
- `internal/core/approvals`, `events`, `destination` — the approval record, inbound events, and which surface a reply goes to.

Surfaces and administration

- `internal/config` — `config.yaml`: shape, defaults, validation, and every write.
- `internal/commands` — Telegram's slash commands.
- `internal/panel` — the web panel API, browser chat, sign-in, setup, and safe mode.

Capability families (`internal/`)

- `channel/telegram`, `channel/discord`, `channel/webchat` — chat surfaces; `channel/channelutil` is what they share.
- `llm/openaicompat` — every Chat Completions-compatible model provider.
- `google`, `mcp`, `web/tavily` — optional tool integrations.
- `storage/sqlite` — `eggy.db`, every machine-managed record.
- `context/markdown`, `skills` — the owner-facing Markdown documents and skills.
- `repository/github`, `subprocess/localprocess` — cloning and reading repositories; bounded child processes.
- `schedule/local` — schedules and cron.
- `auth/session`, `auth/grants`, `auth/connections` — sign-in primitives, sealed OAuth grants, sealed chat credentials.
- `panel/webui` — the embedded panel assets; `fsutil/atomicfile`, `fsutil/filelock` — durable writes and cross-process locks.

Extensions (`internal/extensions/`)

- `finance`, `agentrunner`, `marketplace` — optional features, each behind one `<name>.enabled` key; when off they cost no schema, route, goroutine, or prompt bytes. Their files in shared packages are named `ext_<name>.go`.

## Development

Eggy requires Go 1.26 and Bun for the embedded web asset build.

```sh
make fmt vet race build
```

Run locally:

```sh
cp .env.example .env
cp config.example.yaml config.yaml
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggyd
```

`eggyd` creates missing first-boot files through the existing config
initialization path. The separate administration CLI has been retired.

## Configuration and persistence

Secrets come from environment variables named by `config.yaml`; secret values
must not be copied into the YAML file.

The home directory (normally `/data` on Railway) contains:

- `config.yaml` for startup configuration;
- `SOUL.md`, `memories/USER.md`, `memories/MEMORY.md`, and
  `memories/WATCH.md` for owner-readable context;
- `eggy.db` for every machine-managed record: conversation and thread memory,
  turn traces, runtime state and approvals, schedules, and encrypted Google
  and MCP OAuth grants;
- `skills/` for reviewed procedural skill files;
- `runs/` for bounded, read-only repository checkouts;
- `logs/` for runtime logs.

A `config.yaml` written by an older build is upgraded in place on load, in both
directions: settings whose behaviour has been removed are dropped, and
`calendar.timezone` is carried over to `agent.timezone`; sections this build
has started reading that the file never named are added at their defaults, with
a comment introducing each one. Every change is logged. A backfill never
changes what a config means -- it writes the defaults the absence already
implied -- and a section the owner wrote is left alone, an empty block
included. A config that is already current is left exactly as written.

## Safe mode

When startup fails — usually a `config.yaml` that does not parse or does not
validate — `eggyd` does not exit. It serves the web UI in safe mode instead:
`/healthz` stays healthy so the deployment keeps receiving traffic, `/readyz`
reports the failure, and the authenticated owner gets the startup error plus an
editor for `config.yaml`. A saved config is only written once it loads, so a
second bad config cannot lock the owner out; once one does load, Eggy retries
startup in the same process, with no redeploy.

Safe mode needs `EGGY_UI_USER_EMAIL`, `EGGY_UI_PASSWORD`, and
`EGGY_ENCRYPTION_KEY` to be signed into. It serves nothing else: no chat, no
agent, no memory.

See the [architecture guide](docs/src/content/docs/project/architecture.md) for
dependency rules and [TODO.md](TODO.md) for unfinished simplification work.
