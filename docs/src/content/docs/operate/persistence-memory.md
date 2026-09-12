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
| `SOUL.md` | Durable agent identity, shared by every account |
| `accounts/<id>/memories/USER.md` | That account's context |
| `accounts/<id>/memories/MEMORY.md` | That account's curated durable memory |
| `accounts/<id>/memories/WATCH.md` | That account's heartbeat watch list |
| `memories.migrated/` | The pre-accounts documents, kept as rollback after a migration |
| `eggy.db` | Every machine-managed record: conversation and thread memory, traces, runtime selections, usage, approvals, schedules, and encrypted OAuth grants |
| `skills/` | Reviewed procedural Markdown skills |
| `runs/` | Bounded read-only repository checkouts |
| `logs/` | `gateway.log` and `errors.log` with secret redaction |

Owned subdirectories are secured to mode `0700`; managed files use restrictive permissions.

Machine-managed records are all in `eggy.db`, which is what "SQLite for everything machine-managed" means in practice: one file to back up, one place a record can be, and one transaction behind a change. A home written before that consolidation also holds `state.json`, `cron/`, and `auth.json`. The first boot of a build that has it imports each one inside a single transaction and then renames the source aside as `state.json.migrated`, `cron.migrated`, and `auth.json.migrated`. The import is recorded, so later boots skip it; an interrupted import is retried whole rather than half-applied, and a source left behind by a crash between the commit and the rename is archived on the next boot instead of imported twice.

Records written before accounts existed carry no account until a boot names one: the single owner on a legacy deployment, or `migration_owner_id` on one converted to accounts. The mapping is recorded and a conflicting retry is refused; the pre-accounts `memories/` directory is copied under that account, verified, and archived as `memories.migrated/`. A build from before accounts refuses the upgraded database.

To roll back to a build from before the consolidation, stop the daemon, rename the `.migrated` artifacts back to their original names, and start the older binary. It reads those files and ignores the tables, so nothing has to be exported. Anything written since the migration lives only in `eggy.db` and will not be there.

## Conversation memory

Successful direct turns persist user and assistant messages in embedded SQLite. Failed model calls and non-conversation paths do not become successful history.

`/clear` removes recent conversation history for that conversation. It does not delete `SOUL.md`, owner memory files, or other durable memory.

The model can call `recall_conversation` explicitly. Recall is bounded to ten results and is never silently injected into every prompt.

Within one turn, the live window is governed by a context budget rather than a step cap: work the loop itself produced can be folded into a checkpoint, while the instructions, durable context, the request, and any steering are preserved. See [Long turns, steering, and stopping](/eggy/use/long-turns/).

Turn traces live in `eggy.db` too, bounded by `tracing.keep_turns` and `tracing.retention` — see [Reading traces](/eggy/use/traces/).

## Schedules and skills

Exact and five-field cron schedules live in `eggy.db`. A schedule can run a read-only agent turn or deliver a deterministic message without a model call.

One `schedule` tool covers the subject: `action=list` reports what exists, `action=create` takes an instruction plus either `cron` (recurring) or `at` (one-time RFC3339), and `action=cancel` removes one by id. So a schedule created in conversation can be reviewed and taken back there too. The web panel lists the same schedules and cancels them; creating one stays conversational. The unprompted allowlist grants only `schedule:list` — a heartbeat may see what else is due, but may not change it, and on that turn the tool is described to the model with the list action alone.

Skills are local Markdown procedures. The prompt receives only skill summaries; the model loads full instructions by exact name with `skill_read`. A skill cannot grant a tool or bypass policy, and changing one takes effect on the next turn without a restart. See [Skills](/eggy/use/skills/) and [Schedules and heartbeat](/eggy/configure/automation/).

## Backups

On Railway, back up the mounted `/data` volume. Keep `EGGY_ENCRYPTION_KEY` alongside your secret-management backup: the encrypted OAuth records are not usable without it.

A backup has two halves, and both are needed:

- **`eggy.db`**, the machine-state authority. It runs in WAL mode, so copying the file alone while the daemon is running can capture a torn database. Either stop the daemon and copy `eggy.db`, or take a consistent snapshot with `sqlite3 /data/eggy.db ".backup /path/to/eggy-backup.db"`, which is safe against a running writer. Restore by stopping the daemon and putting the file back at `/data/eggy.db` — remove any leftover `eggy.db-wal` and `eggy.db-shm` beside it first.
- **The owner's own files**: `config.yaml`, `.env`, `SOUL.md`, `memories/`, and `skills/`. These are ordinary text; copy and restore them as files.

A restore is complete when the daemon starts and `/status` reports the approval mode you expect. `EGGY_ENCRYPTION_KEY` is not in either half — restoring a database without it leaves the OAuth grants unreadable, and re-authorizing each provider is the only repair.
