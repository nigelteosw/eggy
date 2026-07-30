---
title: Local development
description: Set up Eggy's Go and Bun toolchains, run the daemon, and work within its package boundaries.
eyebrow: Project
---

Eggy requires Go 1.26 and Bun. Git and Docker are needed for repository integration and container smoke tests.

## Install dependencies

Go modules are declared at the repository root. The embedded browser application has its own Bun package under `website/`.

```sh
go mod download
cd website && bun install
```

## Build and run

```sh
make build
cp config.example.yaml config.yaml
cp .env.example .env
EGGY_CONFIG="$PWD/config.yaml" ./bin/eggyd
```

Use `--home` to keep all runtime artifacts in an explicit local directory.

## Focused development

Put behavior tests beside the package being changed and run the narrow test first:

```sh
go test ./internal/commands
go test ./plugins/tools/mcp
```

Adapter tests should use fake HTTP transports or subprocesses rather than real provider credentials.

## Dependency rules

Keep the kernel and ports provider-neutral. Wire only in bootstrap. Keep configuration mutation in `internal/config`, direct command behavior in `internal/commands`, and HTTP behavior in `internal/web`.

Do not introduce a web framework, ORM, dependency-injection framework, agent framework, native plugin runtime, or external database.
