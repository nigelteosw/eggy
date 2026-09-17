---
title: Configuration overview
description: Understand Eggy's YAML, environment variables, first-boot generation, and restart behavior.
eyebrow: Configure
---

Eggy reads `config.yaml` from its home directory, from `EGGY_CONFIG`, or from an explicit `--config` flag. Unknown YAML fields are rejected so misspellings cannot silently disable a restriction.

## Top-level sections

| Section | Purpose |
| --- | --- |
| `server` | Listen address, public URL, Telegram webhook path, proxy hop count |
| `data_dir` | Durable artifact root |
| `owner` | Canonical single-owner identity (legacy shape) |
| `telegram` | Optional numeric Telegram owner (legacy shape) |
| `accounts` | The people who may use this Eggy, each with a Google address and optional Telegram and Discord identities |
| `web` | The Google sign-in client for accounts |
| `agent` | Default model alias and timezone |
| `providers` | Model adapter connections and catalog discovery |
| `models` | Owner-facing model aliases and their reasoning efforts |
| `repositories` | Trusted read-only Git repositories |
| `runner` | Checkout root, timeout, retention, output, and environment bounds |
| `mcp` | Optional trusted remote or local servers |
| `google` | Optional Google Workspace grant, Eggy's expected identity, products, and approval overrides |
| `tavily` | Optional web search and page extraction |
| `discord` | Optional Discord DM bot; the token is set from the panel, not here |
| `heartbeat` | Optional periodic check-in that speaks only when warranted |
| `approvals` | Where a fresh deployment's approval mode starts |
| `appearance` | Web panel theme |
| `tracing` | Turn traces: the prompt behind every model call and every tool call |

Each section links to the guide that explains it:
[accounts](/eggy/configure/accounts/),
[model providers](/eggy/configure/model-providers/),
[MCP servers](/eggy/configure/mcp-servers/),
[Google Workspace](/eggy/configure/google-workspace/),
[web search](/eggy/configure/web-search/),
[Discord](/eggy/configure/discord/),
[repositories](/eggy/configure/repositories/),
[schedules and heartbeat](/eggy/configure/automation/), and
[approvals](/eggy/use/approvals/).

## Heartbeat

The heartbeat and its watch list have their own guide:
[Schedules and heartbeat](/eggy/configure/automation/). Omitted, the section
costs nothing at runtime — no ticker, no goroutine, no model call.

## Appearance

```yaml
appearance:
  theme: dark   # or light
```

`dark` is the default, so an absent section needs no migration. It lives in YAML
rather than in the browser because the owner is one person across several
devices: a preference kept in `localStorage` is a preference set again on every
laptop they log in from. **Settings → Appearance** writes it.

It is the one config section that changes nothing at runtime — no adapter reads
it, no tool schema depends on it — so unlike every other section it takes effect
on the next page load rather than on restart.

## Approvals

```yaml
approvals:
  mode: normal   # strict | normal | auto
```

This decides only where a *fresh* deployment starts. A mode you have chosen with
`/mode` or in the panel is durable runtime state and outranks the file from then
on — otherwise every restart would undo your choice. See
[Approvals and protected actions](/eggy/use/approvals/).

## Tracing

A trace is one turn as it actually ran: every model call with the exact prompt that produced it, every tool call with its arguments and its output, in the order they happened. The transcript shows what Eggy said; a trace shows what it did to get there. Read them in the web panel under **Traces** — see [Reading traces](/eggy/use/traces/).

Tracing is **on unless you turn it off**:

```yaml
tracing:
  enabled: true
  keep_turns: 500
  retention: "168h"
  max_body_bytes: 1048576
```

Bodies are recorded in full, which is the point — a truncated prompt is exactly the part you wanted to read. The two ceilings are what make that affordable, and both are enforced: `keep_turns` bounds how many traces are retained regardless of age, `retention` drops older ones even when the count is under that cap, and pruning runs as each turn completes rather than on a background timer. `max_body_bytes` is a safety valve on one recorded prompt or tool output, not a budget: a truncated body says so in the record.

A prompt is the most sensitive document Eggy holds — it carries SOUL.md, USER.md, MEMORY.md and your recent conversation — so traces are stored in the same SQLite database as your messages, served only behind the owner session, and passed through the same secret redaction that guards durable context before they are written. Nothing in the agent's own context ever reads a trace back, so a recorded prompt cannot feed itself into the next one.

The settings panel has a **Tracing** page that edits this section as a form, including a **Restore defaults** button — a blank field there means "use the default", so restoring is the ordinary save with the fields emptied rather than a separate reset path. Like every other section, it writes `config.yaml` and applies on the next restart.

A config written before this section existed gains it, at these defaults, the next time Eggy loads — the same mechanism that removes settings a build has stopped reading, run in the other direction. Backfilling never changes what a config means: it writes the defaults the absence already implied, so the file starts describing a setting that was already in force.

Setting `enabled: false` removes the whole capability: no recorder, no model wrapper, no stored rows, and the panel's Traces view is absent rather than empty.

## Secrets

YAML names secret environment variables but does not hold their values:

```yaml
providers:
  deepseek:
    adapter: openai_compatible
    base_url: https://api.deepseek.com
    api_key_env: DEEPSEEK_API_KEY
```

Eggy loads process variables and then `.env`. Secret values are collected for log redaction and never returned through the web config API.

## First boot

When the configured path does not exist, Eggy atomically generates a valid baseline config from deployment variables. Later boots load the persisted file; changing first-boot variables does not rewrite existing YAML.

## Applying changes

The web settings panel writes supported sections directly to `config.yaml`. Nothing in the running process re-reads that file, because bootstrap builds providers, tools, channels, and routes once at startup. A write takes effect on the next restart, and both surfaces say so on every write.

Procedural skills are not YAML configuration: they live as reviewed Markdown files under `skills/` in the Eggy home, and take effect on the next turn rather than on restart — see [Skills](/eggy/use/skills/). Schedules are not configuration either, and are machine-managed records in `eggy.db`, created and cancelled through the `schedule` tool or the web panel — see [Schedules and heartbeat](/eggy/configure/automation/).

## Restarting to apply config

Two surfaces perform that restart: `/restart` in chat, and the **Restart** button on the settings panel's Advanced page. They are the same mechanism — one restart authority, two views onto it — so a restart the command would refuse the button refuses identically. Either way a config you edited from a phone can be applied from a phone.

It is not a process restart: `eggyd` supervises the daemon rather than being it, so the command tears the daemon down and builds a new one from the file on disk inside the same process. Nothing redeploys, the container is never replaced, and the platform never sees the service go away.

What happens, in order:

1. **The config is loaded first, and a bad one cancels the restart.** Both surfaces run the same load startup would, with the same environment including `.env`. If the file would not load, nothing restarts: the reply is the error, and the Eggy you already had keeps running. This matters because a config that fails to load puts the process into safe mode — a repair page served in place of Eggy, with Telegram gone and only the browser able to reach it. That is a recovery path worth having, but not one to fall into from a phone by accident.
2. **In-flight work finishes.** The restart is a signal to the event loop, not a cancellation. The loop stops accepting new work and waits for running turns — including the one that is still owed the `/restart` acknowledgement, which is why the confirmation always arrives. A long tool call delays the restart until it is done.
3. **The old daemon closes down.** MCP clients disconnect and the conversation database closes, then the HTTP listener shuts down gracefully so requests already in flight get their responses — including the panel's own restart request, which is why the button gets an answer rather than a dropped connection.
4. **A new daemon is built from the current `config.yaml`.** Providers, model aliases, MCP servers, repositories, the heartbeat, the scheduler, and the listen address are all rebuilt from what the file says now.

Durable state is on the volume, not in memory, so it survives: conversation history, memory, approvals, the auto-mode setting, schedules, and thread-attached checkouts are all reattached by the new daemon exactly as a redeploy would leave them.

The panel goes quiet while this happens: its chat stream is served by the daemon being replaced, so reload the page once the new one is listening.

Two things a restart does not fix. `.env` is read once when the process starts, so a changed secret still needs a real process restart. And a config that fails to load *later* — one that passed the pre-flight but broke during construction, such as an unreachable repository — lands in safe mode like any other failed startup, which is the recovery path rather than a dead container.
