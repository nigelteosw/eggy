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
| `SOUL.md` | Durable agent identity |
| `memories/USER.md` | Owner context |
| `memories/MEMORY.md` | Curated durable memory |
| `memories/WATCH.md` | The heartbeat's watch list |
| `eggy.db` | Every machine-managed record: conversation and thread memory, traces, runtime selections, usage, approvals, schedules, and encrypted OAuth grants |
| `skills/` | Reviewed procedural Markdown skills |
| `runs/` | Bounded read-only repository checkouts |
| `logs/` | `gateway.log` and `errors.log` with secret redaction |

Owned subdirectories are secured to mode `0700`; managed files use restrictive permissions.

Machine-managed records are all in `eggy.db`, which is what "SQLite for everything machine-managed" means in practice: one file to back up, one place a record can be, and one transaction behind a change. A home written before that consolidation also holds `state.json`, `cron/`, and `auth.json`. The first boot of a build that has it imports each one inside a single transaction and then renames the source aside as `state.json.migrated`, `cron.migrated`, and `auth.json.migrated`. The import is recorded, so later boots skip it; an interrupted import is retried whole rather than half-applied, and a source left behind by a crash between the commit and the rename is archived on the next boot instead of imported twice.

To roll back to a build from before the consolidation, stop the daemon, rename the `.migrated` artifacts back to their original names, and start the older binary. It reads those files and ignores the tables, so nothing has to be exported. Anything written since the migration lives only in `eggy.db` and will not be there.

## Conversation memory

Successful direct turns persist user and assistant messages in embedded SQLite. Failed model calls and non-conversation paths do not become successful history.

`/clear` removes recent conversation history for that conversation. It does not delete `SOUL.md`, owner memory files, or other durable memory.

The model can call `recall_conversation` explicitly. Recall is bounded and is never silently injected into every prompt.

## Schedules and skills

Exact and five-field cron schedules live in `eggy.db`. A schedule can run a read-only agent turn or deliver a deterministic message without a model call.

One `schedule` tool covers the subject: `action=list` reports what exists, `action=create` takes an instruction plus either `cron` (recurring) or `at` (one-time RFC3339), and `action=cancel` removes one by id. So a schedule created in conversation can be reviewed and taken back there too. The web panel lists the same schedules and cancels them; creating one stays conversational. The unprompted allowlist grants only `schedule:list` — a heartbeat may see what else is due, but may not change it, and on that turn the tool is described to the model with the list action alone.

Skills are local Markdown procedures. The prompt receives only enabled skill summaries; the model loads full instructions by exact name with `skill_read`. A skill cannot grant a tool or bypass policy.

## Backups

On Railway, back up the mounted `/data` volume. Keep `EGGY_ENCRYPTION_KEY` alongside your secret-management backup: the encrypted OAuth records are not usable without it.

A backup has two halves, and both are needed:

- **`eggy.db`**, the machine-state authority. It runs in WAL mode, so copying the file alone while the daemon is running can capture a torn database. Either stop the daemon and copy `eggy.db`, or take a consistent snapshot with `sqlite3 /data/eggy.db ".backup /path/to/eggy-backup.db"`, which is safe against a running writer. Restore by stopping the daemon and putting the file back at `/data/eggy.db` — remove any leftover `eggy.db-wal` and `eggy.db-shm` beside it first.
- **The owner's own files**: `config.yaml`, `.env`, `SOUL.md`, `memories/`, and `skills/`. These are ordinary text; copy and restore them as files.

A restore is complete when the daemon starts and `/status` reports the approval mode you expect. `EGGY_ENCRYPTION_KEY` is not in either half — restoring a database without it leaves the OAuth grants unreadable, and re-authorizing each provider is the only repair.
