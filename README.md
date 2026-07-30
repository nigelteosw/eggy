# Eggy

Eggy is a single-owner personal agent written in Go. One daemon serves
Telegram and an optional authenticated web chat, routes requests to configured
model providers, keeps conversation/context memory, and exposes a small tool
registry.

The repository boundary is intentionally read-only. Eggy can clone configured
repositories, inspect files and GitHub metadata, and keep a checkout attached
to a conversation. It cannot edit repository files, run an agent shell,
commit, push, or create pull requests.

## Owner surfaces

Telegram is for conversation, protected-action approvals, and ordinary inline
choices. Its command surface is deliberately limited to:

- `/help`
- `/status`
- `/stop`
- `/clear`
- `/model [alias]`

The agent can call `telegram_select` with its own prompt and 2–8 labelled
options. A tap returns the selected value as the owner's next ordinary message.
Selections are transient, expire after ten minutes, and never authorize a
protected action.

The authenticated web UI provides chat and the one runtime administration
surface. Its settings panel edits providers, model aliases, the default
calendar, and MCP servers directly in `config.yaml`; changes take effect after
restarting `eggyd`.

## Calendar

Native Google Calendar is the one compiled-in product capability, because its
mutations carry approvals a configured MCP server cannot express: creating,
moving, or deleting an event requests an approval bound to that one event's
payload, and approving it authorizes nothing else.

Reads (`calendar_list`, `calendar_calendars`) run directly and cover every
non-hidden readable calendar. Mutations (`calendar_create`, `calendar_update`,
`calendar_delete`) only ever return `awaiting_owner`; the change happens after
the owner approves. Relative ranges like `today` and `this_week` are resolved
against `agent.timezone` on Eggy's own clock, not the model's.

Set `calendar.default_calendar` in `config.yaml`, provide `GOOGLE_CLIENT_ID`,
`GOOGLE_CLIENT_SECRET`, and `EGGY_ENCRYPTION_KEY`, then connect the account at
`/auth/google`. Omit the `calendar` section and Calendar is entirely absent —
no tools, no OAuth routes, no prompt bytes.

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

## Development

Eggy requires Go 1.26 and Bun for the embedded web asset build.

```sh
make fmt vet test race build
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
- `SOUL.md`, `memories/USER.md`, and `memories/MEMORY.md` for owner-readable
  context;
- `eggy.db` for conversation/thread memory;
- `state.json`, `auth.json` (encrypted Calendar and MCP OAuth credentials), and
  `cron/` for remaining operational records pending the SQLite consolidation
  tracked in `TODO.md`;
- `skills/` for reviewed procedural skill files;
- `runs/` for bounded, read-only repository checkouts;
- `logs/` for runtime logs.

A `config.yaml` written by an older build is upgraded in place on load:
settings whose behaviour has been removed are dropped, and `calendar.timezone`
is carried over to `agent.timezone`. Each drop is logged. A config with nothing
retired in it is left exactly as written.

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
