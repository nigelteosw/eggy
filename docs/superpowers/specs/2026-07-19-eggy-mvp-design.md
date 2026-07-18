# Eggy MVP Design

## Purpose

Eggy is a single-user personal agent that runs continuously on Railway and is reachable through Telegram. It combines a general personal-assistant loop powered by DeepSeek with a specialized Codex coding backend, allowing its owner to continue repository work from a phone while also using Calendar, reminders, memory, and proactive check-ins.

The MVP must remain a small, understandable Go harness. It is open for extension through stable ports and adapters, while the kernel is closed to provider-specific modification.

## MVP success criterion

From Telegram, the owner can ask Eggy to work on a configured repository, observe progress, inspect the result, and separately approve commit, push, and pull-request creation. Eggy can also answer Calendar questions, request approval for Calendar mutations, remember durable context in `MEMORY.md`, execute scheduled work, and send bounded proactive check-ins.

## Scope

The MVP includes:

- A headless Railway service named `eggyd`.
- A companion CLI named `eggy`, structured so it can become a TUI later.
- A Telegram webhook adapter with a single allowlisted user.
- DeepSeek V4 Flash for ordinary assistant turns and V4 Pro for automatic escalation.
- A minimal model tool loop owned by Eggy.
- A Codex CLI adapter for repository implementation, testing, and debugging.
- A same-container local-process runner for trusted repositories.
- GitHub access through a personal access token.
- Google Calendar OAuth, reads, and approval-gated writes.
- Exact schedules, cron-style recurring work, and heartbeat-style proactive turns.
- Human-readable file-backed configuration, state, and memory on a Railway Volume.
- Docker and Railway deployment artifacts.

The MVP excludes:

- Multiple users.
- Discord and other chat channels.
- A web UI or full TUI.
- PostgreSQL, SQLite, embeddings, or vector search.
- Native Go `.so` plugins or an external extension protocol.
- A separate execution service or strong sandbox for untrusted code.
- Automatic merge, direct protected-branch pushes, or self-deployment.
- Gmail and Google services other than Calendar.

## Architectural style

Eggy uses a ports-and-adapters modular monolith.

The kernel contains domain types and use-case orchestration only. It does not import Telegram, DeepSeek, Codex, GitHub, Google, YAML, JSON-file persistence, or Railway packages. Provider implementations sit behind small Go interfaces. A composition root reads configuration, constructs adapters, validates capabilities, and injects them into kernel services.

Initial adapters are compiled into one Go binary and selected through configuration. Adding an adapter may change the composition package and binary assembly, but it must not change the kernel. A language-neutral external adapter protocol may be introduced later without replacing kernel concepts.

## Project structure

```text
cmd/
├── eggyd/                  Railway daemon entry point
└── eggy/                   CLI entry point and future TUI host

internal/
├── kernel/
│   ├── events/             Normalized event types
│   ├── agent/              Assistant loop and model routing
│   ├── tasks/              Durable work lifecycle
│   ├── approvals/          Side-effect approval lifecycle
│   └── services/           Application use cases
├── ports/                  Stable interfaces owned by the kernel
├── adapters/
│   ├── models/deepseek/    DeepSeek Chat Completions adapter
│   ├── channels/telegram/  Telegram webhook and delivery adapter
│   ├── memory/markdown/    MEMORY.md adapter
│   ├── state/jsonfile/     Atomic state.json adapter
│   ├── scheduler/local/    Exact, cron, and heartbeat scheduler
│   ├── coding/codexcli/    codex exec JSONL adapter
│   ├── runner/localprocess Same-container workspace runner
│   ├── repositories/github Git and GitHub REST adapter
│   └── calendar/google/    Google OAuth and Calendar REST adapter
└── bootstrap/              Configuration and dependency composition
```

## Kernel ports

The kernel defines and depends on these capabilities:

- `Model`: generate or stream an assistant turn with tool definitions and messages.
- `Tool`: describe, validate, and execute one agent capability.
- `Channel`: receive normalized messages and deliver responses or approvals.
- `MemoryStore`: load and safely update durable agent memory.
- `StateStore`: atomically read and mutate operational state.
- `Scheduler`: calculate and persist the next execution time for scheduled work.
- `TriggerSource`: emit normalized events from time or an external system.
- `CodingAgent`: start, continue, interrupt, and stream a coding-agent run.
- `Runner`: create, inspect, execute within, and destroy a workspace.
- `RepositoryProvider`: clone, inspect, commit, push, and create pull requests.
- `CalendarProvider`: authorize and read or mutate Calendar events.
- `ApprovalPolicy`: classify an action and enforce authorization before execution.

Interfaces remain small and use kernel-owned request and result types. Provider SDK types must not cross adapter boundaries.

## Runtime data model

Every input is normalized into an `Event` containing an ID, type, source, owner, timestamp, correlation ID, and typed payload. Telegram messages, scheduler ticks, heartbeats, OAuth callbacks, and runner updates use the same dispatcher.

Operational state in `/data/state.json` includes:

- Conversation summaries and recent messages.
- Registered repositories and selected repository.
- Agent turns and coding-run lifecycle records.
- Action-specific approval requests and decisions.
- Exact, recurring, and heartbeat schedule definitions.
- Calendar OAuth metadata and encrypted refresh tokens.
- Telegram update idempotency keys.
- Proactive-message counts and quiet-hour state.

The state schema has an explicit version. The JSON-file adapter serializes writes with a process lock, checks the expected version, writes a temporary file, syncs it, and atomically renames it. This design supports only one `eggyd` replica.

## Configuration and secrets

Persistent volume contents:

```text
/data/
├── config.yaml
├── state.json
├── MEMORY.md
└── codex/
```

`config.yaml` contains non-secret static settings:

- Single Telegram owner ID.
- Public base URL and webhook path.
- Model adapter names and model IDs.
- Flash-to-Pro escalation thresholds.
- Repository names, clone URLs, base branches, and protected branches.
- Runner limits and allowed child-process environment variables.
- Quiet hours, heartbeat cadence, and proactive-message limits.
- Calendar defaults.

Secrets are supplied as environment variables loaded from an uncommitted `.env` locally and Railway variables in deployment:

- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_WEBHOOK_SECRET`
- `DEEPSEEK_API_KEY`
- `GITHUB_TOKEN`
- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `EGGY_ENCRYPTION_KEY`

Only `.env.example` is committed. Google refresh tokens are encrypted before entering `state.json`. Provider credentials remain inside their adapters and are not included in model prompts, state snapshots, or logs.

## Memory

The MVP has one durable `/data/MEMORY.md` file. It holds stable personal preferences, project context, procedural guidance, and significant unfinished follow-ups in human-editable Markdown.

All access occurs through `MemoryStore`. The assistant may propose controlled append or section-replacement operations, which the adapter validates and applies atomically. Eggy does not create daily memory files, embeddings, or a database-backed memory index in the MVP.

Memory content is context, not authority. Current user instructions and repository-owned `AGENTS.md` files override conflicting memory.

## Assistant and model routing

Eggy owns a small bounded tool loop for personal-assistant work. It sends the current request, compact conversation context, relevant `MEMORY.md` content, and registered tool schemas to the configured `Model`.

DeepSeek V4 Flash is the default for:

- Telegram conversation.
- Intent and coding-task routing.
- Calendar and GitHub read workflows.
- Memory update proposals.
- Schedule creation.
- Heartbeat evaluation.

DeepSeek V4 Pro is selected automatically when deterministic policy detects a complex non-coding request, a configured tool-step threshold, or repeated recoverable tool failures. The owner may also request Pro explicitly. A turn can escalate only once.

Coding requests are handed to `CodingAgent`; DeepSeek does not micromanage Codex file or command operations.

## Coding workflow

For a repository task:

1. Flash identifies a coding intent and resolves a configured repository.
2. `Runner` creates `/tmp/runs/<run-id>` with a strict lifecycle and timeout.
3. `RepositoryProvider` clones the configured base branch using temporary PAT-backed credentials.
4. The runner confirms the checkout and locates repository `AGENTS.md` guidance.
5. `CodexCLIAdapter` starts `codex exec --json` in the workspace.
6. Codex owns repository inspection, editing, tests, debugging, and final review.
7. Eggy parses Codex JSONL into normalized progress events and streams concise updates to Telegram.
8. The runner returns status, diff, validation evidence, and a proposed commit message.
9. Eggy creates an approval for commit.
10. Commit, push, and pull-request creation each require a separate approval.
11. The workspace is destroyed after completion, rejection, expiration, or retention timeout.

Direct pushes to configured protected branches are rejected regardless of approval. The MVP never merges pull requests.

## Codex authentication

The Railway service sets `CODEX_HOME=/data/codex`. The owner performs a one-time `codex login --device-auth` from a Railway service shell and authorizes it through a browser. Codex-managed authentication survives container replacement because `/data` is a Railway Volume.

The adapter invokes the Codex CLI non-interactively with JSONL output, an explicit working directory, workspace-write sandboxing, and bounded execution. Codex authentication is not copied into `.env` or `state.json`.

Because Codex and repository commands run in the same container, this design is only for trusted repositories. Environment sanitation and path restrictions reduce accidental exposure but do not form a security boundary against malicious repository code.

## Telegram

Telegram is the only channel in the MVP. `eggyd` exposes a webhook endpoint and verifies both Telegram's webhook secret and the configured owner ID. Updates are deduplicated using Telegram update IDs.

The adapter maps Telegram messages and callback queries into kernel events. It renders text responses, progress updates, errors, and approval actions. Telegram-specific message and callback types do not enter the kernel.

Initial commands are limited to operational shortcuts such as status, repositories, runs, stop, schedules, memory, and new conversation. Natural-language requests remain the primary interface.

## GitHub

The MVP uses a personal access token supplied through `GITHUB_TOKEN`. The token should be fine-grained and limited to configured repositories with the minimum contents and pull-request permissions required.

The GitHub adapter performs repository metadata and pull-request API operations. Git clone and push use a temporary credential helper or askpass mechanism so the token is not embedded in clone URLs, command arguments, Git configuration, diffs, or logs.

## Google Calendar

The Google adapter exposes an OAuth start endpoint and callback. Refresh tokens are encrypted at rest using `EGGY_ENCRYPTION_KEY`.

Calendar reads may execute automatically. Create, update, and delete operations create an approval containing the exact calendar, time range, participants, and fields to change. An approved operation is idempotent and must not silently apply a materially different mutation if state changed before approval.

## Scheduler and Keep-On

Time is a `TriggerSource`. The scheduler persists three kinds of work:

- Exact one-time tasks.
- Cron-style recurring tasks.
- Heartbeat windows for contextual proactive turns.

The scheduler, not an LLM, owns timing and next-run calculation. A due item emits an event. Exact and recurring jobs execute their stored instruction. A heartbeat asks Flash to inspect bounded current state, schedules, Calendar context, and `MEMORY.md`, then either produce no action or send a proactive Telegram message.

Heartbeats respect configured quiet hours, minimum intervals, and weekly message limits. They cannot commit, push, open pull requests, or mutate Calendar without the normal approval flow.

## Approvals and policy

Inspection, editing, dependency installation, and test execution are allowed automatically for configured trusted repositories.

These actions require independent Telegram approval:

- Commit.
- Push.
- Pull-request creation.
- Calendar create, update, or delete.

Approvals are action-specific, expire, and bind to a payload digest. A changed diff, branch, commit, or Calendar mutation invalidates the old approval. The kernel consults `ApprovalPolicy` immediately before every protected operation.

## Safety and failure handling

- Startup validates configuration and selected adapter capabilities before serving traffic.
- Missing optional configuration disables that adapter; invalid required configuration fails startup.
- Tool inputs are schema-validated and unknown tools are rejected.
- Telegram retries are idempotent.
- Network retries are bounded and apply only to transient failures.
- Authentication and validation failures are not retried automatically.
- Runner commands have timeouts, output limits, cancellation, and process-group termination.
- Repository subprocesses receive an allowlisted environment.
- Interrupted runs are marked `interrupted` after restart.
- Eggy never automatically replays a protected write after restart.
- Flash may escalate to Pro only once per turn.
- User-visible errors are concise; structured logs contain correlation IDs and redacted diagnostics.

## HTTP surface

The MVP exposes:

- `GET /healthz`: process liveness.
- `GET /readyz`: configuration and required-adapter readiness.
- `POST /webhooks/telegram`: Telegram updates.
- `GET /auth/google`: begin Calendar OAuth.
- `GET /auth/google/callback`: complete Calendar OAuth.

No general public agent API or web UI is included. Future TUI and web clients will use a versioned API added behind the same services.

## Testing

The project targets Go 1.26 and uses the standard library where practical. It does not use a web framework, ORM, dependency-injection framework, agent framework, or native plugin runtime.

Verification layers:

- Kernel unit tests use in-memory fake ports for routing, approvals, escalation, restart recovery, and heartbeat limits.
- Adapter contract tests apply shared behavioral expectations to each implementation.
- HTTP adapters use `httptest` servers for Telegram, DeepSeek, GitHub, and Google payloads.
- Process tests use temporary Git repositories and a fake Codex executable that emits JSONL.
- Persistence tests use a temporary data directory to verify atomic state and memory updates.
- A Docker smoke test starts Eggy with fake adapters and checks health and readiness.
- Live credential tests are opt-in and excluded from the default suite.

## Deployment

The repository includes `Dockerfile`, `.dockerignore`, `railway.toml`, `config.example.yaml`, `.env.example`, `Makefile`, `README.md`, and `AGENTS.md`.

The image contains `eggyd`, `eggy`, Codex CLI, Git, CA certificates, and a documented baseline of tooling for trusted repository work. It does not attempt to bundle every language runtime. Additional runner images or a separate runner service remain future extensions.

Railway exposes the HTTP port, mounts persistent storage at `/data`, configures health checks, and provides secrets as service variables. Exactly one `eggyd` replica is supported while `state.json` is the operational store.

## Delivery slices

Implementation proceeds as working vertical slices rather than disconnected adapter stubs:

1. Foundation: binaries, configuration, ports, file stores, health endpoints, container build, and fake-adapter smoke test.
2. Assistant: Telegram ingestion and delivery, DeepSeek tool loop, single-user enforcement, conversation state, and `MEMORY.md`.
3. Keep-On: exact schedules, cron recurrence, heartbeats, quiet hours, and proactive-message limits.
4. Coding: repository registry, local-process runner, Codex JSONL execution, progress delivery, diff capture, and cancellation.
5. Shipping: commit, push, and pull-request adapters with independent approvals and protected-branch enforcement.
6. Calendar: Google OAuth, encrypted refresh-token persistence, Calendar reads, and approval-gated writes.
7. Railway hardening: volume bootstrap, Codex device-login procedure, webhook setup, readiness checks, and deployment documentation.

Each slice must leave the default test suite and Docker smoke test passing. A later slice may extend a port but must not introduce provider-specific concepts into the kernel.

## Extension rules

Every new integration must:

1. Implement an existing small port or justify a new provider-neutral capability.
2. Keep provider-specific types and errors inside the adapter.
3. Register through the composition root.
4. Include contract tests and startup capability validation.
5. Avoid adding provider knowledge to the kernel.

The core harness remains intentionally minimal: event dispatch, bounded agent orchestration, tasks, approvals, and provider-neutral ports.

## Deferred evolution

Likely follow-on work includes:

- Replace JSON state with PostgreSQL through `StateStore`.
- Add a remote runner or sandbox provider through `Runner`.
- Add an external extension protocol without removing compiled adapters.
- Build the `eggy` TUI and a versioned client API.
- Add a web UI over the same API.
- Add Discord as another `Channel`.
- Add richer memory retrieval only after `MEMORY.md` proves insufficient.
