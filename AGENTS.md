# Eggy development guidance

Eggy is a Go 1.26 ports-and-adapters modular monolith.

## Boundaries

- `internal/kernel/services` is the base kernel-service package;
  `internal/kernel/services/repo` holds everything about repositories,
  workspaces, changes, and shipping. The dependency is one-way — `repo` may
  import `services`, never the reverse — so anything `repo` needs from the base
  package must be exported there (see `services.DecodeToolInput`). Test fakes
  are duplicated across the two rather than shared through an exported
  package: a fake is not API.
- Keep `internal/kernel` and `internal/ports` provider-neutral. They must not import Telegram, DeepSeek, Codex, GitHub, Google, YAML, JSON-file persistence, Docker, or Railway packages.
- Provider request/response types and credentials stay inside their adapter packages.
- Register adapters and tools only through `internal/bootstrap`. Bootstrap is the composition root and nothing else: it wires adapters into services and owns the event loop. Config parsing and mutation belong in `internal/config`, the command catalog in `internal/commands`, and the HTTP surface in `internal/web`. The dependency direction is one-way — `config` <- `commands` <- `web` <- `bootstrap` — so none of those three may import `internal/bootstrap`.
- Treat configured repositories as trusted, but keep path, environment, timeout, output, and process-group restrictions intact.
- Never weaken independent approval checks for commit, push, pull-request creation, or Calendar mutations. Protected branches remain unpushable even with approval.

## Adding a new adapter (open for extension, closed for modification)

A new provider (model backend, chat channel, repository host, coding-agent
runner, calendar backend, etc.) should only ever add a new package under
`plugins/<category>/<provider>/` plus a wiring change in
`internal/bootstrap`. It should never require changing `internal/kernel`,
`internal/ports`, or an existing adapter package.

1. Find the port(s) your provider must satisfy in `internal/ports/ports.go`
   (`Model`, `Channel`, `ContextStore`, `StateStore`, `Scheduler`, `Runner` /
   `StreamingRunner`, `CodingRepository`, `RepositoryCommitter`,
   `RepositoryPusher`, `PullRequestProvider`, `RepositoryReader`,
   `RepositoryCapabilityProvider`, `CalendarProvider`, `Tool`, ...), or a
   service-level interface such as `repo.Shipper`
   (`internal/kernel/services/repo/repository_tools.go`). Do not change the
   interface's method signatures to fit one new provider — every existing
   adapter implements them and would break.
2. If the capability is genuinely new (no existing port fits), add a small,
   narrowly-scoped interface to `ports.go` rather than widening an existing
   one, and keep it provider-neutral (no provider-specific types, no
   credentials in the signature).
3. Implement the interface in the new adapter package. Keep that provider's
   wire types, HTTP/CLI calls, and credentials entirely inside the package —
   `internal/kernel` and `internal/ports` must never import it.
4. Wire construction only in `internal/bootstrap` (`app.go`'s `NewApp` for
   constructing the adapter and handing it to the relevant kernel service
   constructor, or `registry.Register` for a new `Tool`). This is the one
   place allowed to know every adapter exists. New config or secret fields go
   in `internal/config` (`config.go`), not in bootstrap.
5. Prefer branching on an existing selector over hardcoding one adapter. Two
   `ProviderConfig.Adapter` (`internal/config/config.go`) is in the config
   for exactly this: it is meant to pick a model adapter per provider instead
   of `app.go` always calling `openaicompat.New`. Route new provider kinds
   through that switch rather than adding another special case.
6. Add adapter-level tests against a fake HTTP server or fake subprocess in
   the new package, plus a `FakeAdapters`-mode path in `app.go` if the
   adapter needs one for `make smoke`/integration tests.

## Workflow

- Add or change behavior test-first and run the focused test before the full suite.
- Prefer the standard library and small interfaces. Do not introduce a web framework, ORM, DI framework, agent framework, native plugin runtime, or database.
- Run `make fmt vet test race build` before completing a change. Run `make smoke` when Docker is available.
- Preserve `/data/state.json` schema compatibility or introduce an explicit migration and schema-version change.
