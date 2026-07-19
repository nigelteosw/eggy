# Eggy development guidance

Eggy is a Go 1.26 ports-and-adapters modular monolith.

## Boundaries

- Keep `internal/kernel` and `internal/ports` provider-neutral. They must not import Telegram, DeepSeek, Codex, GitHub, Google, YAML, JSON-file persistence, Docker, or Railway packages.
- Provider request/response types and credentials stay inside their adapter packages.
- Register adapters and tools only through `internal/bootstrap`.
- Treat configured repositories as trusted, but keep path, environment, timeout, output, and process-group restrictions intact.
- Never weaken independent approval checks for commit, push, pull-request creation, or Calendar mutations. Protected branches remain unpushable even with approval.

## Workflow

- Add or change behavior test-first and run the focused test before the full suite.
- Prefer the standard library and small interfaces. Do not introduce a web framework, ORM, DI framework, agent framework, native plugin runtime, or database.
- Run `make fmt vet test race build` before completing a change. Run `make smoke` when Docker is available.
- Preserve `/data/state.json` schema compatibility or introduce an explicit migration and schema-version change.
