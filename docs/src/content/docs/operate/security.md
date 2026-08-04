---
title: Security model
description: Review Eggy's owner boundary, credential isolation, trusted inputs, approval rules, and process restrictions.
eyebrow: Operate
---

Eggy is designed for one owner and repositories the owner already trusts. It reduces accidental authority and credential exposure; it is not a hostile-code sandbox.

## Owner boundary

Telegram accepts one configured numeric owner. Web chat requires the configured email and password, issues a signed HTTP-only session, and throttles login failures. Every channel maps back to the same canonical `owner.id`.

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

## Scheduled turns

Scheduled agent turns are read-only and do not gain authority from instruction text. Deterministic message schedules do not invoke a model at all.
