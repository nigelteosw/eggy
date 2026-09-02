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
| `owner` | Canonical single-owner identity |
| `telegram` | Optional numeric Telegram owner |
| `agent` | Default model alias and timezone |
| `providers` | Model adapter connections |
| `models` | Owner-facing model aliases |
| `repositories` | Trusted read-only Git repositories |
| `runner` | Checkout root, timeout, retention, output, and environment bounds |
| `mcp` | Optional trusted remote or local servers |
| `heartbeat` | Optional periodic check-in that speaks only when warranted |
| `tracing` | Turn traces: the prompt behind every model call and every tool call |

## Heartbeat

Omitted, the heartbeat costs nothing: no ticker, no model call. Set an interval to turn it on:

```yaml
heartbeat:
  interval: 3h
  # instruction: "Check the deploy and open pull requests."
  # active_hours:
  #   start: "08:00"
  #   end: "22:00"
  # include_recent_history: false
```

`3h` is the recommended starting interval. Each tick runs an isolated read-only turn and delivers to Telegram only when there is something worth saying — so a heartbeat is not one message per tick. A tick is skipped while another turn is running. Without a `telegram` block there is nowhere to deliver unprompted output, so the heartbeat stays off and says so once at startup.

### What a beat looks at

A beat checks `memories/WATCH.md`, the standing list of what you have asked Eggy to keep an eye on. It annotates that list with what it has already reported, and reads those notes on the next beat — which is how it tells "already mentioned this" from "new", and what stops a finding worth reporting once from being reported every interval.

A watch entry is a thing to look at, never a thing with its own cadence. Anything that should happen at a particular time is a schedule, not a watch entry.

**An empty watch list skips the beat entirely, with no model call**, and warns once so the silence is distinguishable from a bug. So an interval alone does nothing until you write down something to watch.

### Active hours

`active_hours` confines beats to a window of your day, read in `agent.timezone` rather than the host's clock. `start` is inclusive, `end` is exclusive, and `"24:00"` is accepted as an end so a window can run to midnight without the wrapped `"00:00"` that would mean the opposite. A window whose `end` is before its `start` wraps midnight, which is how an overnight watch is written. Both bounds are required together, and a malformed window fails the config load rather than silently suppressing every beat.

A beat that would fall inside quiet hours is moved to the window opening rather than dropped, so the first beat of the day arrives at `start` instead of whenever the interval happens to land after it. The gap between beats is also measured from the end of one beat to the start of the next, so a slow check-in does not shorten the interval that follows it.

### Conversation history

`include_recent_history` lets a beat see the recent conversation window, so it can notice that you said you would ship something on Friday. It is **off by default**: unprompted turns carry no ambient history, so your earlier chat cannot silently steer a turn you are not present for and did not review when it fired. Tools stay read-only either way — this changes what a beat knows, never what it can do.

It is the one heartbeat setting no surface writes. Relaxing a safety invariant should cost more than a tap on a phone, so it lives in `config.yaml` only.

The settings panel edits the rest of this section. A blank interval there means off, rather than leaving the previous interval in place; blank active hours likewise clear the window. The instruction is preserved either way, so turning the heartbeat back on does not mean retyping it. Like every other section, the panel writes `config.yaml` and the change applies on the next restart, which `/restart` in chat performs.

## Tracing

A trace is one turn as it actually ran: every model call with the exact prompt that produced it, every tool call with its arguments and its output, in the order they happened. The transcript shows what Eggy said; a trace shows what it did to get there. Read them in the web panel under **Traces**.

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

Schedules and procedural skills are not YAML configuration. They live as reviewed files under `cron/` and `skills/` in the Eggy home.

## Restarting to apply config

Two surfaces perform that restart: `/restart` in chat, and the **Restart** button on the settings panel's Advanced page. They are the same mechanism — one restart authority, two views onto it — so a restart the command would refuse the button refuses identically. Either way a config you edited from a phone can be applied from a phone.

It is not a process restart: `eggyd` supervises the daemon rather than being it, so the command tears the daemon down and builds a new one from the file on disk inside the same process. Nothing redeploys, the container is never replaced, and the platform never sees the service go away.

What happens, in order:

1. **The config is loaded first, and a bad one cancels the restart.** Both surfaces run the same load startup would, with the same environment including `.env`. If the file would not load, nothing restarts: the reply is the error, and the Eggy you already had keeps running. This matters because a config that fails to load puts the process into safe mode — a repair page served in place of Eggy, with Telegram gone and only the browser able to reach it. That is a recovery path worth having, but not one to fall into from a phone by accident.
2. **In-flight work finishes.** The restart is a signal to the event loop, not a cancellation. The loop stops accepting new work and waits for running turns — including the one that is still owed the `/restart` acknowledgement, which is why the confirmation always arrives. A long tool call delays the restart until it is done.
3. **The old daemon closes down.** MCP clients disconnect and the conversation database closes, then the HTTP listener shuts down gracefully so requests already in flight get their responses — including the panel's own restart request, which is why the button gets an answer rather than a dropped connection.
4. **A new daemon is built from the current `config.yaml`.** Providers, model aliases, MCP servers, repositories, the heartbeat, the scheduler, and the listen address are all rebuilt from what the file says now.

Durable state is on the volume, not in memory, so it survives: conversation history, memory, approvals, the auto-mode setting, schedules in `cron/`, and thread-attached checkouts are all reattached by the new daemon exactly as a redeploy would leave them.

The panel goes quiet while this happens: its chat stream is served by the daemon being replaced, so reload the page once the new one is listening.

Two things a restart does not fix. `.env` is read once when the process starts, so a changed secret still needs a real process restart. And a config that fails to load *later* — one that passed the pre-flight but broke during construction, such as an unreachable repository — lands in safe mode like any other failed startup, which is the recovery path rather than a dead container.
