# Eggy architecture

Eggy is a Go ports-and-adapters modular monolith. `cmd/eggyd` starts one
process; `internal/bootstrap` constructs adapters, registers tools, owns the
event loop, and exposes the HTTP handler.

## Dependency direction

Provider-neutral policy lives under `internal/kernel` and interfaces under
`internal/ports`. Neither may import Telegram, GitHub, Google, a model vendor,
Docker, Railway, or concrete persistence packages.

Adapters live under `plugins/<category>/<provider>`. A new provider implements
an existing narrow port and is wired in `internal/bootstrap`. Provider wire
types, credentials, and HTTP/CLI details stay inside the adapter.

The control-plane dependency direction is:

```text
internal/config <- internal/web <- internal/bootstrap
```

`internal/commands` is not a general control plane. It directly dispatches the
five Telegram conversational commands.

## Event and turn flow

```text
Telegram webhook / web chat
        |
        v
provider-neutral event queue
        |
        v
dispatcher -> turn service -> one model/tool loop
        |                         |
        |                         +-> filtered tool registry
        v
routed channel -> originating conversation
```

Telegram callback authentication and update ownership are checked in the
Telegram adapter before an event enters the queue. Approval callbacks become
approval events. `select:<opaque-id>:<index>` callbacks are resolved by the
adapter's short-lived, single-use registry and become ordinary owner messages.
The two callback families never share semantics.

## Tool surface

Bootstrap owns one registry. Kernel tools are provider-neutral; adapter-owned
tools are registered only when their adapter exists. `telegram_select`, for
example, is absent in web-only and fake-adapter mode.

Repository access is read-only. A conversation can attach one durable checkout
with `workspace_open`, inspect it with repository readers, and destroy it with
`workspace_close`. Root containment, bounded reads, timeouts, process-group
cancellation, recovery, expiry, and explicit discard remain enforced.

There is no repository mutation or shipping path: no edit/shell tools, change
store, commit/push/PR ports, write approvals, or automated check polling.
Repository declarations are synchronized from startup config on every boot.

Calendar is the one native product capability. Its tools live beside
`services.CalendarService`, not in bootstrap; bootstrap injects only the
approval-delivery callback the mutation tools need, and keeps Google wire types
and credentials inside `plugins/calendar/google`. An empty `calendar` config
section registers no tools and mounts no OAuth routes.

## Owner control plane

Telegram supports `/help`, `/status`, `/stop`, `/clear`, and `/model`. Unknown
slash commands return concise help and never reach the model.

Authenticated web routes call `internal/config` directly for provider, model,
calendar, and MCP settings. There is no web file browser, shared CLI/Telegram
grammar, or separate `eggy` administration binary.

## Persistence

Current durable forms are:

- YAML: startup configuration;
- Markdown: owner-readable context and procedural skills;
- SQLite: conversations, messages, full-text recall, and thread/workspace
  bindings;
- JSON: legacy operational state, plus encrypted Calendar and MCP OAuth
  credentials in `auth.json`;
- files under `cron/`: schedules;
- directories under `runs/`: bounded, read-only repository checkouts.

The next simplification phase moves remaining machine-managed records into the
existing SQLite database, with an explicit retry-safe migration. State schema
compatibility remains mandatory until that migration lands.

## Approval and selection callbacks

Approval callback handling is an independent, expiring, payload-digest-bound
boundary. Calendar create, update, and delete are the protected actions: each
has its own `approvals.Action` and its own executor, so an approval authorizes
one operation against one payload digest and nothing else. Telegram selections
are single-use ordinary answers on a separate callback prefix and can never
authorize an action.

## Extension rule

A provider addition should normally add one package under `plugins/` and one
bootstrap wiring change. Add a new port only for a genuinely new,
provider-neutral capability. Do not add runtime plugin loading, a DI framework,
an agent framework, an ORM, or generic lifecycle abstractions.
