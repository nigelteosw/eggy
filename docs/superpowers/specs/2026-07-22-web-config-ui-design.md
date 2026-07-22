# Eggy web config UI design

**Status:** Approved for implementation planning
**Date:** 2026-07-22

## Context

Eggy's config (providers, models, calendar) is currently viewable and editable
only through Telegram's `/config` commands and the CLI's `eggy config`
commands, both routed through the same shared `CommandService` and command
catalog (`internal/bootstrap/commands.go`). There is no visual, form-based way
to manage it, and no way to do so without a Telegram client or shell access.

This adds a fourth transport: a small React + Tailwind single-page app,
embedded into and served by the existing Go binary, authenticated by a single
owner credential, talking to a small JSON API that is itself a thin wrapper
over the existing `CommandService` catalog — not a new config-mutation path.

This is the first of several planned additions; later work is expected to
extend the same API/UI shape to repository and MCP server management. This
spec covers only the first slice: providers, models, and calendar, matching
`/config`'s current real scope exactly.

## Goals

- Let the owner view and edit providers, models, and calendar settings through
  a web browser.
- Reuse the exact validation, mutation, and YAML-persistence logic `/config`
  already uses. The web UI must not duplicate config-mutation logic or
  introduce a second source of truth for what a valid provider/model/calendar
  config looks like.
- Keep deployment as one process, one Railway service: no separate frontend
  host, no database, no new session store.
- Gate all access behind a single owner login (email + password from two new
  environment variables), matching Eggy's existing single-owner trust model
  (the same model behind Telegram's owner allowlist).
- Shape the API and routing so later sections (repositories, MCP servers,
  scheduler) can be added as more routes and more frontend cards without
  reworking auth, embedding, or the request/response shape established here.

## Non-goals (this iteration)

- Repository management (add/remove) through the web UI. Stays Telegram/CLI
  with the existing owner-approval flow.
- Multi-user accounts, roles, or password reset flows. A single hardcoded
  owner credential only, matching the single-owner model used everywhere else
  in Eggy.
- Client-side routing or multiple pages. One page, one view, gated by session
  state.
- Frontend automated tests. Deferred; the actual validation logic lives in and
  is tested by the Go backend, which the web API calls directly.

## Architecture

```text
Browser (React + Tailwind, built with Vite)
        |
        v
Eggy's existing http.ServeMux (internal/bootstrap/server.go)
        |
        +-- GET /, /assets/*        -> embedded static build (go:embed)
        +-- POST /api/login         -> checks EGGY_UI_USER_EMAIL / EGGY_UI_PASSWORD,
        |                              sets a signed session cookie
        +-- POST /api/logout        -> clears the session cookie
        +-- GET  /api/session       -> 200 if the session cookie is valid, 401 otherwise
        +-- GET/POST /api/config/providers
        +-- GET/POST /api/config/models
        +-- GET/POST /api/config/calendar
                |
                v
        existing CommandService / command catalog
        (handleConfigGetProviders, handleConfigSetProvider, ...)
```

A new adapter package, `internal/adapters/web/`, owns the embedded static
assets (`//go:embed dist`) and the session/auth logic (cookie signing and
verification, login rate limiting). It is wired only from
`internal/bootstrap/web.go`, matching the existing rule that adapters are
registered only in `internal/bootstrap`.

Frontend source lives in a new top-level `web/` directory (sibling to `cmd/`,
`internal/`, `docs/`), built with `bun run build` into `web/dist`, which
`internal/adapters/webui` embeds. The `Makefile` gains a `build-web` target
that runs before `go build` in both local dev and the Docker build; the
Dockerfile gains a Bun build stage ahead of the existing Go build stage.
Bun is a build-time dependency only — the deployed container remains a
single Go binary, one process, one Railway service, matching the existing
"exactly one `eggyd` replica, file-backed state" constraint.

## Authentication

Two new environment variables, resolved through the existing `getenv`/
`Secrets` pattern (`internal/bootstrap/config.go`), never stored in YAML:

- `EGGY_UI_USER_EMAIL`
- `EGGY_UI_PASSWORD`

`POST /api/login` compares the submitted email and password against these two
values with `crypto/subtle.ConstantTimeCompare`. No hashing: the credential
already lives in plaintext in the environment, the same trust level as
`TELEGRAM_WEBHOOK_SECRET` today, so server-side hashing would add no real
protection.

On success, Eggy sets an HttpOnly, Secure, SameSite=Strict cookie containing
an HMAC-SHA256-signed token (issued-at and expiry, signed with the existing
`EGGY_ENCRYPTION_KEY` — no third secret, no session store, fully stateless).
The cookie expires 12 hours after issue with no sliding renewal; after
expiry, the owner logs in again. `POST /api/logout` clears the cookie
immediately.

Failed logins are throttled in-memory, keyed by remote IP address (Railway
terminates the connection at its own edge, so `RemoteAddr` is good enough for
a single-owner deployment — this is not meant to defend against distributed
brute force, only casual guessing): after 5 failed attempts from the same IP
within a 15-minute window, each further attempt from that IP is delayed by 2
seconds before Eggy responds. The counter resets on a successful login for
that IP or after 15 minutes with no attempts. This state is in-memory only
and resets on restart, which is acceptable for this threat model.

Every `/api/config/*` route requires a valid session cookie; a missing,
malformed, expired, or signature-mismatched cookie returns 401.

## API and data flow

Each `/api/config/<section>` GET or POST handler builds a `CommandRequest`
identical in shape to what Telegram's and the CLI's parsers already build,
dispatches it through the existing `catalogIndex` / `CommandService.dispatch`,
and renders the resulting `CommandResult` through a new `RenderJSON` method
added to `CommandResult` alongside the existing `RenderPlainText` and
`RenderMarkdown` — a third renderer for the same structured result, not a
fourth ad-hoc response format.

HTTP status is derived from `CommandResult.State`:

| `CommandResult.State`   | HTTP status |
|-------------------------|-------------|
| `success`, `info`       | 200         |
| `warning`               | 200 (with a `warning` field set in the body) |
| `error`, `help`         | 400         |
| (auth failure)          | 401         |
| (unexpected error)      | 500         |

The response body is a direct JSON projection of `CommandResult`'s existing
fields (`state`, `title`, `detail`, `fields`, `next`, ...), not a new schema
invented per endpoint.

## Frontend structure

Vite + React + TypeScript + Tailwind. No component library, no client-side
router — a single page with two states:

- `App`: on mount, calls `GET /api/session`. Renders `LoginPage` on 401,
  `ConfigPage` on 200.
- `LoginPage`: an email/password form posting to `/api/login`, showing
  whatever error text the backend returns.
- `ConfigPage`: three stacked cards — `ProvidersCard`, `ModelsCard`,
  `CalendarCard` — each fetching its own section on mount, rendering a form
  pre-filled with current values, and posting edits to the matching route.
  Backend validation errors render inline on the card that produced them; a
  successful save shows a brief inline confirmation.

## Error handling

Backend: `RenderJSON` reuses `CommandResult`'s existing error/help/warning
semantics, so the web UI surfaces the same validation messages Telegram and
the CLI already produce — there is no separate error-message set to keep in
sync across three surfaces.

Frontend: an inline error banner scoped to the form that failed. A 401 from
any request redirects to `LoginPage`. An unexpected network error (no
response, non-JSON body) shows a generic "something went wrong, try again"
message rather than a raw error dump.

## Testing

Backend (Go), table-driven, matching existing test style:

- Login success, login failure, and the IP-keyed backoff (including reset on
  success and after the window expires).
- Cookie signing, verification, expiry, and tamper rejection (a flipped byte
  or expired timestamp must be rejected).
- Each `/api/config/*` route round-tripping through the real `CommandService`,
  proving the web path and the existing Telegram/CLI path produce the same
  validation outcome for the same input — a parity test in the same spirit as
  the existing Telegram/CLI parity tests.
- Auth middleware rejecting every `/api/config/*` route without a valid
  session cookie.

Frontend: manual verification only for this iteration; no unit test framework
is introduced. The logic that actually needs testing (validation, mutation,
persistence) lives in and is already tested by the Go backend the frontend
calls.

`make fmt vet test race build` must pass, with `make build-web` (or
equivalent) running before `go build` in both local dev and the Docker build.
`make smoke` runs when Docker is available and must include the frontend
build stage, not assume pre-built assets.

## Implementation sequence constraints

1. Add behavior test-first, starting with cookie sign/verify and the login
   check, since every other route depends on that auth layer.
2. Keep all HTTP-serving, embedding, and session logic inside
   `internal/adapters/web/`; wire construction, routes, and the two new env
   vars only in `internal/bootstrap`.
3. Do not change `CommandService`, the command catalog, or `CommandResult`'s
   existing fields to fit the web UI — add only a new `RenderJSON` method,
   following the same pattern as the two existing renderers.
4. Do not add a database, session store, or a new encryption key — reuse the
   existing `EGGY_ENCRYPTION_KEY` for cookie signing.
5. Do not implement repository management through the web UI; it is
   explicitly out of scope per Non-goals above.
6. Verify with `make fmt vet test race build`; run `make smoke` when Docker is
   available, confirming the frontend build stage runs as part of it.

## Extension: MCP server management (added after the initial iteration)

The MCP client design's "config stays file-owned, no add/edit/remove
command" decision was deliberately reopened for the web UI only, after
confirming scope with the owner: Telegram and the CLI still have no way to
add, edit, or remove an MCP server definition — only the web UI does, and
only through dedicated routes, not `CommandService`/the command catalog
(there is still no `/config get|set mcp` command).

- `internal/bootstrap/config_mutate.go` gained `SetMCPServer` (upserts by
  name), `RemoveMCPServer`, and `GetMCPServersConfig`, following the exact
  pattern `SetProvider`/`SetModelAlias`/`SetCalendar` already established
  (`filelock.With` for the whole load-mutate-write sequence, `cfg.Validate()`
  before persisting, atomic write via `writeConfigUnlocked`).
- The web form exposes only the essential fields: `name`, `url`, `auth`
  (`oauth`/`bearer-env`/`none`), `enabled`, and `bearer_token_env` when
  `auth` is `bearer-env`. `transport` is fixed to `streamable-http` (the
  only supported value) and is not user-facing. Advanced fields the form
  doesn't expose — `oauth_scopes`, `tool_filter`, `connect_timeout`,
  `timeout`, `max_output_bytes` — are preserved untouched when editing an
  existing server, and get the same sane defaults `Config.applyDefaults`
  would give a new one, so a freshly added server validates immediately.
- `internal/bootstrap/web.go` gained `GET/POST /api/config/mcp` and
  `DELETE /api/config/mcp/{name}`, calling the mutation helpers directly
  (there is no catalog entry to bridge through for this section, unlike
  providers/models/calendar).
- `web/src/McpCard.tsx` lists configured servers in a table (reusing the
  same `table_headers`/`table_rows` shape `CommandResult` already produces
  for providers/models) with a per-row Remove button, plus a form to add or
  update one server by name.
- Saved changes still require an owner restart to take effect, and an
  oauth-mode server still needs `/mcp login <name>` via Telegram/CLI
  afterward — the web UI only edits `config.yaml`, exactly like Telegram's
  `/config set provider|model|calendar` already do; it never talks to the
  runtime `internal/adapters/tools/mcp` manager directly.
