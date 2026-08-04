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
- `/mcp [add|remove|enable|disable|login|logout]`
- `/mode [strict|normal|auto]`
- `/restart`

The agent can call `telegram_select` with its own prompt and 2–8 labelled
options. A tap returns the selected value as the owner's next ordinary message.
Selections are transient, expire after ten minutes, and never authorize a
protected action.

`/mcp` is the one administration command, and it is there because the config
it edits lives on the Eggy runtime — an owner on a phone cannot shell into the
deployment to add a server. It can list servers with their live state, add,
remove, enable, disable, and start or discard an OAuth authorization. No secret
value is ever accepted as a chat argument: `bearer_env` and `client_secret_env`
name environment variables, and their values must already exist in the
deployment's environment.
stdio servers stay file-only, since a subprocess command line is not a chat
argument.

The authenticated web UI provides chat and a settings panel for providers,
model aliases, and MCP servers, plus `/auth/mcp/{server}` to start an OAuth
flow in the browser.

Both surfaces are views onto one administration authority: every config write
goes through `internal/config` under the same file lock with the same
validation. Writes take effect after restarting `eggyd`, and both surfaces say
so. `/restart` and the panel's Restart button perform that restart through one
shared path: it rebuilds the daemon from the file on disk inside the running
process, after checking the new config loads and letting in-flight turns
finish. Config edited from a phone is therefore applicable from a phone, with
no redeploy.

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
- `state.json`, `auth.json` (encrypted MCP OAuth credentials), and
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
