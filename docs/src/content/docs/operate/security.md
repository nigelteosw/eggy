---
title: Security model
description: Review Eggy's owner boundary, credential isolation, trusted inputs, approval rules, and process restrictions.
eyebrow: Operate
---

Eggy is designed for one owner and repositories the owner already trusts. It reduces accidental authority and credential exposure; it is not a hostile-code sandbox.

## Account boundary

Every request and turn acts as one account, resolved at a trusted ingress and never from anything the caller sends: a verified browser session, a verified numeric Telegram sender in a private chat, or a schedule's stored owner. Private records — conversations, memory, watch list, schedules, traces, approvals — are keyed by account in the database and on disk, and every read and write fails closed without one. A cross-account request answers as if the record did not exist. Ownership is checked before the approval mode is consulted, so `auto` never bypasses it, and an approval is answered and consumed only by the account that asked. See [Accounts](/eggy/configure/accounts/).

With accounts configured, web chat requires Google Sign-In (OpenID Connect with PKCE and a nonce, verified server-side; no login token is kept), issues an opaque HTTP-only session whose hash alone is stored, and checks a per-session CSRF header plus same-origin on every mutating route. A single-owner deployment keeps the configured email and password with a signed HTTP-only session, and throttles login failures. Telegram accepts only configured numeric senders, in private chats.

Eggy's Google connection is its own Workspace user, verified against `google.expected_email` before a grant is stored. Eggy holds no grant on any person's Google account. Anything Eggy can reach through its own account is shared by everyone who uses it.

## Secrets

Secret values come from environment variables or `.env`, not YAML. Provider credentials remain inside adapters. Logger setup receives the loaded secret set and redacts it from output.

MCP OAuth and Google records are sealed with AES-256-GCM under `EGGY_ENCRYPTION_KEY`, which also signs web UI session cookies. One sealing implementation covers every provider record (`plugins/auth/authfile`), and session signing lives beside it in `plugins/auth/session`.

Owner authentication and outbound authorization are deliberately separate. `plugins/auth/session` answers who may talk to Eggy; the OAuth grants under `plugins/tools/` answer what Eggy may do on the owner's behalf.

## Repository boundary

Configured repositories are trusted. Their tools remain path-restricted, timeout-bounded, output-bounded, and environment-allowlisted. The current general agent surface is read-only.

The local runner is a restricted process boundary, not a container boundary. A stdio MCP server runs as Eggy's operating-system user and must be treated as trusted code.

## Side effects

MCP servers are trusted at configuration time: a server's tools run without asking unless you say otherwise. Telegram selections cannot authorize mutations.

`require_approval` is how you say otherwise. Naming a tool under a server's `require_approval` routes each of its calls through the payload-bound approval mechanism: the call does not reach the server, the approval binds the exact arguments, and your decision executes it once. Eggy cannot judge which of an arbitrary server's tools are dangerous, so the list is yours to write — narrow what a server exposes with `tool_filter`, then gate the mutations that remain.

`/mode auto` disables every gate until the mode is changed back. It is durable across restarts and `status` names it, but it is a real bypass: in auto mode a gated tool is exactly as trusted as an ungated one. `/mode strict` is the other end — every tool call asks, reading included.

## Skills and traces

A skill is a Markdown file you placed in `skills/`. That placement is the review,
which is why Eggy has no skill installer or marketplace: fetching agent-readable
instructions from the internet would delete the security model. A skill is text
the agent reads, never something Eggy executes, and it grants no tool and lifts
no approval. See [Skills](/eggy/use/skills/).

Traces record the exact prompt behind every model call, which makes them the most
sensitive records Eggy holds — a prompt carries `SOUL.md`, `USER.md`, `MEMORY.md`,
and recent conversation. They are stored in the same `eggy.db` as messages, served
only behind the owner session, and passed through the same secret redaction that
guards durable context before they are written. Nothing in the agent's own context
reads a trace back, so a recorded prompt cannot feed itself into the next one.

## Recovering from a bad config

A startup failure serves the [safe-mode](/eggy/operate/safe-mode/) repair page
rather than exiting. It is owner-authenticated like the normal panel, serves no
chat, agent, or memory, and can change nothing but `config.yaml` — and only
through a candidate the config loader has already accepted.

## Scheduled turns

Scheduled agent turns and heartbeat beats are read-only, carry no ambient conversation history unless `heartbeat.include_recent_history` is set, cannot reach MCP, and do not gain authority from instruction text. The `schedule` tool is offered to them with the `list` action alone. Deterministic message schedules do not invoke a model at all.

Only direct owner turns accept steering. A message arriving during an unprompted turn cannot redirect work the owner was not present for.
