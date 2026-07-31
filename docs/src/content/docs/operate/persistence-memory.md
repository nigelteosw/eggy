---
title: Persistence and memory
description: Understand every durable artifact in Eggy's home and what survives restarts, clears, and redeploys.
eyebrow: Operate
---

Eggy resolves one home directory before loading configuration. An explicit `--home` wins, then `EGGY_HOME`, then the directory containing `EGGY_CONFIG`, and finally `/data`.

## Home layout

| Path | Contents |
| --- | --- |
| `config.yaml` | Startup configuration |
| `.env` | Local secrets |
| `auth.json` | Encrypted MCP OAuth credentials |
| `SOUL.md` | Durable agent identity |
| `memories/USER.md` | Owner context |
| `memories/MEMORY.md` | Curated durable memory |
| `eggy.db` | Conversation and thread memory |
| `state.json` | Runtime selections, usage, and approvals |
| `cron/` | One readable YAML file per schedule |
| `skills/` | Reviewed procedural Markdown skills |
| `runs/` | Bounded read-only repository checkouts |
| `logs/` | `gateway.log` and `errors.log` with secret redaction |

Owned subdirectories are secured to mode `0700`; managed files use restrictive permissions.

## Conversation memory

Successful direct turns persist user and assistant messages in embedded SQLite. Failed model calls and non-conversation paths do not become successful history.

`/clear` removes recent conversation history for that conversation. It does not delete `SOUL.md`, owner memory files, or other durable memory.

The model can call `recall_conversation` explicitly. Recall is bounded and is never silently injected into every prompt.

## Schedules and skills

Exact and five-field cron schedules live under `cron/`. A schedule can run a read-only agent turn or deliver a deterministic message without a model call.

Skills are local Markdown procedures. The prompt receives only enabled skill summaries; the model loads full instructions by exact name with `skill_read`. A skill cannot grant a tool or bypass policy.

## Backups

On Railway, back up the mounted `/data` volume. Keep `EGGY_ENCRYPTION_KEY` alongside your secret-management backup: the encrypted OAuth records are not usable without it.
