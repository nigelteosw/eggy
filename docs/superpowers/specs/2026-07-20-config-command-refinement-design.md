# Config Command Refinement Design

## Goal

Replace `/config set`'s positional argument syntax with order-independent `key=value` flags, add `calendar` as a fourth settable section, add `/config get path`, and add a CLI-only `eggy config show` for a full-file dump. This is a refinement of the `/config` command shipped in `docs/superpowers/specs/2026-07-19-config-command-design.md` — it does not change what `/config` is for, only how it's typed and what it covers.

## Motivation

The positional syntax shipped last round (`/config set provider <name> <adapter> <base_url> <api_key_env>`) is error-prone in Telegram: no autocomplete, no inline help, and getting the argument order wrong produces a usage error only after typing the whole command. `key=value` flags remove the "which order was it again" failure mode without requiring new architecture — no session state, no change to message routing, no multi-turn conversation.

Separately, Calendar (`calendar.enabled`, `calendar.default_calendar`, `calendar.timezone`) was left out of the original whitelist alongside `telegram.owner_id` and `server.*`, on the reasoning that a bad value there is dangerous. On reflection that grouping was wrong: unlike owner ID or the webhook path, a bad Calendar value can't lock the owner out of the bot or break Telegram delivery — worst case a Calendar command fails with a clear error until corrected. It belongs in `/config` too.

Two other directions were considered and explicitly rejected this round:
- **Auto-deriving Calendar's enabled state** from either Railway secret presence or completed OAuth enrollment, instead of a manual toggle. Rejected because Hermes Agent — the reference this whole `/config` feature is modeled on — keeps tool/integration activation (its `hermes tools` toggle) explicitly independent of credential presence; credentials being present does not auto-enable a tool. Eggy's existing `calendar.enabled` manual toggle already matches that pattern. No schema or boot-time behavior change ships in this round.
- **A full guided multi-turn wizard** (bot asks for each field one at a time). Rejected as disproportionate to the actual complaint — the pain is argument *order*, not the absence of a conversational flow, and a wizard needs new per-owner session state and a change to how plain-text replies get routed. `key=value` flags solve the order problem without that cost.

## Scope

In scope:
- Rewrite `/config set` parsing for the three existing sections (`coding_agent`, `provider`, `model`) from positional to `key=value` flags.
- Add `calendar` as a fourth `/config set`/`/config get` section, covering exactly the three fields `CalendarConfig` already has (`enabled`, `default_calendar`, `timezone`) — no new fields, no schema change.
- Add `/config get path`, printing the config file's location.
- Add `eggy config show`, matching Hermes's `hermes config show` — dumps the *entire* config file as YAML, not just the four whitelisted sections. Safe to expose in full: `config.yaml` never holds secret values, only environment-variable *names* (`api_key_env`, `credential_env`), so there's nothing in it that isn't already meant for the owner's eyes.

  **CLI-only, deliberately not a Telegram command.** Every other `/config` command is identical on Telegram and the `eggy` CLI because both call through the same shared `CommandService.Execute` dispatcher (`internal/bootstrap/commands.go`). `show` breaks that pattern on purpose: it's implemented directly in `cmd/eggy/main.go`'s argument handling — using the same `-config`/`EGGY_CONFIG` path `main()` already resolves before calling `bootstrap.NewApp`, not a value obtained from `App` or `CommandService` — calling a small `bootstrap` package function to read and re-marshal the file, and never touches `CommandService.Execute` or `App.ExecuteCommand` at all. That's not just a routing choice — it's the only way to guarantee a full-file dump can never be reached through the Telegram webhook, since Telegram only ever calls into the shared dispatcher. There is deliberately no `/config show` Telegram command.

Out of scope, deliberately:
- No change to `Config`, `CalendarConfig`, `Config.Validate()`, boot-time secret requirements, `/calendar_auth` gating, or the capability manifest. This round only changes how existing, already-true behavior gets *typed* — Calendar continues to work exactly as it does today, just becomes settable through `/config` instead of only through a manual YAML edit.
- No change to `/repositories add`, `/coding_agent`, `/model`, or any command outside `/config`.
- No multi-turn wizard, no Telegram inline-keyboard buttons.
- `/config get`/`/config set` keep operating only on the whitelist (`coding.agents.*`, `providers.*`, `models.*`, `calendar.*`); `telegram.owner_id`, `server.*`, and `runner.*` remain untouchable from chat, unchanged from the original design.

## Command syntax

Telegram and `eggy` CLI, identically (via `CommandService.Execute`):

```text
/config get coding
/config get providers
/config get models
/config get calendar
/config get path

/config set coding_agent alias=<alias> adapter=<codex_cli|claude_cli> [credential_env=<ENV_NAME>]
/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>
/config set model alias=<alias> provider=<provider> model=<model_id>
/config set calendar [enabled=<true|false>] [default_calendar=<id>] [timezone=<IANA timezone>]
```

`eggy` CLI only (never reachable from Telegram — see Scope):

```text
eggy config show
```

The `/config set` syntax change fully replaces the positional syntax shipped last round — there is no dual-syntax support. The feature is hours old with a single operator (the owner); keeping two parsers for the same command adds confusion for no compatibility benefit.

## Parsing rules

- Everything after the section keyword (`coding_agent`, `provider`, `model`, `calendar`) is parsed as `key=value` tokens, in any order.
- Each token is split on the *first* `=` only, so a value that itself contains `=` (a `base_url` with a query string, for instance) still parses correctly.
- An unrecognized key, for that section, produces a usage error naming the valid keys for that section.
- `coding_agent`, `provider`, and `model` insert or overwrite a whole map entry — every key is required except `coding_agent`'s `credential_env`, matching the optionality that already exists today. A missing required key produces a usage error naming what's missing.
- `calendar` patches the existing `CalendarConfig` struct field-by-field: any subset of `enabled`/`default_calendar`/`timezone` may be given, and only those fields change — `/config set calendar enabled=true` alone does not clear `default_calendar` or `timezone` if they were already set. At least one key must be present; zero keys is a usage error.
- `enabled` is parsed as `strconv.ParseBool` (accepts `true`/`false`/`1`/`0`/`t`/`f`, matching Go's standard behavior — no reason to be stricter than the language's own parser here).

## Validation

Unchanged from the original design: after applying the mutation, the full `Config.Validate()` runs before anything is written — the same structural checks already enforced at boot (`validateCoding`, `validateProviders`, and the existing `calendar.enabled && default_calendar == ""` check). Invalid input leaves the file untouched and returns the exact validation error.

## `/config get path`

Returns the config file's own path (the value passed as `-config` / `EGGY_CONFIG`). No new mutation logic — this is a read of a value `CommandService` already holds, not a new file operation.

## Security

Unchanged from the original design: owner-only, whitelist-restricted, no secret values ever appear in `/config get` output or `/config set` confirmations (only adapter names, URLs, and environment-variable *names*), atomic write wrapped in `filelock.With` covering the full load-mutate-write sequence.

## Testing

Test-first, per repository convention:
- Failing tests for `key=value` parsing of all four `/config set` sections: valid input, unknown key, missing required key, and (for `coding_agent`) the optional `credential_env` both present and absent.
- A test confirming `/config set calendar enabled=true` alone leaves a previously-set `default_calendar`/`timezone` unchanged (patch semantics), and a test confirming Calendar's existing validation (`enabled: true` requires `default_calendar`) still rejects invalid combinations.
- A test for `/config get calendar` output format and `/config get path`.
- A test that `eggy config show` prints the full file (all sections, not just the whitelist) and, separately, a test confirming `CommandService.Execute` has no `"show"` case reachable under `/config` — `show` only exists in `cmd/eggy/main.go`'s CLI argument handling.
- Existing tests from the original `/config` round that assert the *old* positional syntax must be rewritten to the new syntax, not deleted silently — the underlying `setCodingAgent`/`setProvider`/`setModelAlias` mutation functions and their validation/atomic-write/concurrency guarantees are unchanged and keep their existing test coverage.

Required completion checks, unchanged:

```text
make fmt vet test race build
```
