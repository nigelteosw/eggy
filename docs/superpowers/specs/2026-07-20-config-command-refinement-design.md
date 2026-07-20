# Config Command Refinement Design

## Goal

Replace `/config set`'s positional argument syntax with order-independent `key=value` flags on Telegram, add `calendar` as a fourth settable section, and give the `eggy` CLI its own separate `config get`/`set`/`show` implementation with real CLI flags — no longer routed through the same dispatcher Telegram uses. This is a refinement of the `/config` command shipped in `docs/superpowers/specs/2026-07-19-config-command-design.md` — it does not change what `/config` is for, only how each interface types it and what it covers.

This round also establishes the pattern later commands should follow when they get the same CLI/Telegram split: a shared, interface-agnostic business-logic layer that's closed for modification, with each interface (Telegram, CLI, anything added later) as a separate, thin adapter that's open for extension. See "Architecture: splitting CLI from Telegram" below.

## Motivation

The positional syntax shipped last round (`/config set provider <name> <adapter> <base_url> <api_key_env>`) is error-prone in Telegram: no autocomplete, no inline help, and getting the argument order wrong produces a usage error only after typing the whole command. `key=value` flags remove the "which order was it again" failure mode without requiring new architecture — no session state, no change to message routing, no multi-turn conversation.

Separately, Calendar (`calendar.enabled`, `calendar.default_calendar`, `calendar.timezone`) was left out of the original whitelist alongside `telegram.owner_id` and `server.*`, on the reasoning that a bad value there is dangerous. On reflection that grouping was wrong: unlike owner ID or the webhook path, a bad Calendar value can't lock the owner out of the bot or break Telegram delivery — worst case a Calendar command fails with a clear error until corrected. It belongs in `/config` too.

Separately again: while designing `eggy config show` (a full-file dump, safe only because `config.yaml` never holds secret values), it became clear the reason it needed special handling — routing it around the shared Telegram/CLI dispatcher — is a symptom of a real constraint, not a one-off. Telegram and the CLI have different needs (message-length and typing-ergonomics limits vs. a real terminal with `--help` and no length cap), and forcing both through one parser means neither can be well-suited to its own interface. Rather than special-case just `show`, this round splits `/config`'s CLI surface from its Telegram surface entirely, while keeping the underlying mutation/validation logic — `config_mutate.go` — exactly as shared as it already was.

Two other directions were considered and explicitly rejected this round:
- **Auto-deriving Calendar's enabled state** from either Railway secret presence or completed OAuth enrollment, instead of a manual toggle. Rejected because Hermes Agent — the reference this whole `/config` feature is modeled on — keeps tool/integration activation (its `hermes tools` toggle) explicitly independent of credential presence; credentials being present does not auto-enable a tool. Eggy's existing `calendar.enabled` manual toggle already matches that pattern. No schema or boot-time behavior change ships in this round.
- **A full guided multi-turn wizard** (bot asks for each field one at a time). Rejected as disproportionate to the actual complaint — the pain is argument *order*, not the absence of a conversational flow, and a wizard needs new per-owner session state and a change to how plain-text replies get routed. `key=value` flags solve the order problem without that cost.

## Scope

In scope:
- Rewrite Telegram's `/config set` parsing for the three existing sections (`coding_agent`, `provider`, `model`) from positional to `key=value` flags.
- Add `calendar` as a fourth `/config set`/`/config get` section (Telegram) and `config set calendar`/`config get calendar` (CLI), covering exactly the three fields `CalendarConfig` already has (`enabled`, `default_calendar`, `timezone`) — no new fields, no schema change.
- Add `/config get path` (Telegram) and `eggy config get path` (CLI).
- Add `eggy config show`, matching Hermes's `hermes config show` — dumps the *entire* config file as YAML, not just the four whitelisted sections. Safe to expose in full: `config.yaml` never holds secret values, only environment-variable *names* (`api_key_env`, `credential_env`).
- Split `/config`'s CLI implementation from its Telegram implementation entirely (see Architecture below). `eggy config ...` stops being routed through `CommandService.Execute`/`App.ExecuteCommand`; it becomes its own flag-based implementation in `cmd/eggy/config.go`, calling the same exported `config_mutate.go` functions Telegram calls.
- Export the `config_mutate.go` functions (`setCodingAgent` → `SetCodingAgent`, etc.) so `cmd/eggy` (package `main`) can call them directly, since Go visibility requires it.

Out of scope, deliberately:
- No change to `Config`, `CalendarConfig`, `Config.Validate()`, boot-time secret requirements, `/calendar_auth` gating, or the capability manifest. This round only changes how existing, already-true behavior gets *typed* — Calendar continues to work exactly as it does today, just becomes settable through `/config` instead of only through a manual YAML edit.
- No change to `/repositories add`, `/coding_agent`, `/model`, `/status`, `/usage`, or any command outside `/config` — those keep sharing `CommandService.Execute`/`App.ExecuteCommand` between Telegram and CLI exactly as they do today. Splitting them the same way `/config` is split this round is future work, following the pattern this round establishes, not something this round does.
- No multi-turn wizard, no Telegram inline-keyboard buttons.
- `/config get`/`/config set` (both interfaces) keep operating only on the whitelist (`coding.agents.*`, `providers.*`, `models.*`, `calendar.*`); `telegram.owner_id`, `server.*`, and `runner.*` remain untouchable from either interface, unchanged from the original design. (`eggy config show` is the one deliberate exception — see Scope entry above — and it is read-only.)

## Architecture: splitting CLI from Telegram

Three layers, going forward, for any command that gets this treatment:

1. **Business logic** — plain Go functions with no knowledge of Telegram or the CLI: take primitive/struct parameters, return a value or an error. For `/config`, this is `internal/bootstrap/config_mutate.go`: `SetCodingAgent(path, alias, adapter, credentialEnv string) error`, `SetProvider(...)`, `SetModelAlias(...)`, `SetCalendar(...)`, `GetCodingConfigText(path string) (string, error)`, `GetProvidersConfigText(...)`, `GetModelAliasesConfigText(...)`, `GetCalendarConfigText(...)`, `ShowConfigText(path string) (string, error)`. This layer owns validation, atomic writes, and every correctness guarantee (the `filelock`-covered load-mutate-write sequence, `Config.Validate()`). It does not change when a new interface is added — closed for modification.
2. **Telegram interface** — `CommandService.Execute` in `internal/bootstrap/commands.go`. Parses a Telegram message string into a call against layer 1, formats the result as chat text. Unchanged in kind from the original `/config` design, just calling the newly-exported function names and adding the `calendar` section.
3. **CLI interface** — a new file, `cmd/eggy/config.go` (package `main`, sibling to `main.go`). Parses `os.Args` for the `config` subcommand using `flag.NewFlagSet` per verb, into a call against the same layer-1 functions, formats the result as terminal text with real `--help` output (`flag.FlagSet.PrintDefaults`).

Adding a new interface later (or splitting a different command the same way) means writing a new thin layer-2/3-style adapter that calls the existing layer-1 functions — open for extension, without touching layer 1 or the other interface's code.

`cmd/eggy/main.go`'s `run()` gains one branch: if `flags.Arg(0) == "config"`, dispatch to `configMain` in `cmd/eggy/config.go` instead of building an `App` and calling `ExecuteCommand`. Building a full `App` (adapters, runner, services) is unnecessary for config commands — they only ever touch the file at `*configPath`, so `cmd/eggy/config.go` calls the layer-1 functions directly against that path, without constructing an `App` at all. Every other CLI command (`status`, `repositories`, `runs`, ...) keeps going through the existing `bootstrap.NewApp` + `app.ExecuteCommand` path unchanged.

## Command syntax

Telegram (`CommandService.Execute`, `key=value` flags):

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

`eggy` CLI (`cmd/eggy/config.go`, standard `--flag=value` syntax, kebab-case):

```text
eggy config get coding
eggy config get providers
eggy config get models
eggy config get calendar
eggy config get path
eggy config show

eggy config set coding-agent --alias=<alias> --adapter=<codex_cli|claude_cli> [--credential-env=<ENV_NAME>]
eggy config set provider --name=<name> --adapter=openai_compatible --base-url=<url> --api-key-env=<ENV_NAME>
eggy config set model --alias=<alias> --provider=<provider> --model=<model_id>
eggy config set calendar [--enabled=<true|false>] [--default-calendar=<id>] [--timezone=<IANA timezone>]

eggy config set provider --help    # prints flag descriptions via flag.FlagSet.PrintDefaults
```

There is no dual-syntax support within an interface — Telegram only ever speaks `key=value`, the CLI only ever speaks `--flag=value`. The two interfaces are allowed to diverge in syntax now that they're separate implementations; "similar functionality" means both can do the same four operations (get/set on four sections, plus CLI-only show), not that they read identically.

## Parsing rules

**Telegram**, unchanged in kind from the previous round:
- Everything after the section keyword (`coding_agent`, `provider`, `model`, `calendar`) is parsed as `key=value` tokens, in any order, each split on the *first* `=` only (so a `base_url` containing `=` in a query string still parses).
- An unrecognized key produces a usage error naming the valid keys for that section.
- `coding_agent`, `provider`, `model` require every key except `coding_agent`'s `credential_env`. `calendar` requires at least one key, treats each as an independent patch to the existing struct (see below).

**CLI**, via `flag.NewFlagSet(verb, flag.ContinueOnError)` per subcommand:
- Same required/optional split as Telegram, expressed as `flag.StringVar`/`flag.BoolVar` registrations instead of manual token parsing.
- An empty required flag (Go's `flag` package doesn't distinguish "not passed" from "passed as empty string" without extra bookkeeping) is treated as missing — `cmd/eggy/config.go` checks each required flag's value is non-empty after `Parse()` and reports which ones, the same information a `flag.ErrHelp` usage message gives.
- `--help`/`-h` on any subcommand prints that subcommand's flags via `PrintDefaults()`, standard Go `flag` behavior.

**Shared** (both interfaces, enforced in layer 1 — `config_mutate.go` — so it can't drift between them):
- `calendar` patches the existing `CalendarConfig` struct field-by-field: any subset of `enabled`/`default_calendar`/`timezone` may be given, and only those fields change — setting `enabled` alone does not clear `default_calendar` or `timezone` if they were already set.
- `enabled` is parsed as `strconv.ParseBool` (accepts `true`/`false`/`1`/`0`/`t`/`f`).

## Validation

Unchanged from the original design: after applying the mutation, the full `Config.Validate()` runs before anything is written — the same structural checks already enforced at boot (`validateCoding`, `validateProviders`, and the existing `calendar.enabled && default_calendar == ""` check). Invalid input leaves the file untouched and returns the exact validation error, on both interfaces, because both call the same layer-1 function.

## `config get path` and `eggy config show`

`get path` returns the config file's own path — on Telegram, the value `CommandService` already holds (`configPath`); on the CLI, `*configPath` already resolved in `main()`. No new mutation logic on either side.

`eggy config show` re-marshals the whole loaded `Config` as YAML via the existing `Config.MarshalYAML()` and prints it. CLI-only — see Scope and Architecture above for why it's structurally impossible to reach from Telegram once `eggy config` no longer routes through `CommandService.Execute` at all.

## Security

Unchanged from the original design: owner-only (Telegram authorization happens at the webhook layer before dispatch; CLI+file access is already an equivalent trust level), whitelist-restricted on both interfaces for `set`/non-`show` `get`, no secret values ever appear in output (only adapter names, URLs, and environment-variable *names*), atomic write wrapped in `filelock.With` covering the full load-mutate-write sequence — all enforced once, in layer 1, so neither interface can accidentally weaken it.

## Testing

Test-first, per repository convention:
- `internal/bootstrap/config_mutate_test.go`: rename call sites to the newly-exported function names; add `SetCalendar`/`GetCalendarConfigText`/`ShowConfigText` coverage (valid input, patch-only-given-fields semantics, existing `Config.Validate()` rejections) alongside the existing `SetCodingAgent`/`SetProvider`/`SetModelAlias` tests, which keep their current coverage unchanged in substance.
- `internal/bootstrap/commands_test.go`: rewrite the `/config set` tests from positional to `key=value` syntax; add `/config get calendar`, `/config get path`, `/config set calendar` cases; add a test confirming `CommandService.Execute` has no `"show"` case reachable under `/config` (`show` must not exist on Telegram).
- New `cmd/eggy/config_test.go`: flag parsing for each `config set` verb (valid input, missing required flag, unknown flag, `--help` output), `config get <section>`, `config get path`, and `config show`, all exercised without constructing a full `App` (per Architecture, `cmd/eggy/config.go` never calls `bootstrap.NewApp`).

## Documentation

`README.md` currently documents the positional `/config set` syntax and the fact that `/config` is identical on Telegram and the CLI (both from the original round). Both statements become wrong this round: the operational-shortcuts line and the Railway Claude Code section need the new `key=value` Telegram syntax, and the CLI examples need to show `eggy config set ... --flag=value` instead of the old shared syntax, plus a mention of `eggy config show`.

Required completion checks, unchanged:

```text
make fmt vet test race build
```
