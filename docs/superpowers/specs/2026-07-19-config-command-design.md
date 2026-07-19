# Config Command Design

## Goal

The owner can register new `coding.agents`, `providers`, and `models` entries in `config.yaml` without SSH access to the Railway container. A Hermes-style `/config get` / `/config set` command, exposed identically through Telegram and the `eggy` CLI, replaces manually editing the persisted YAML over `railway ssh`.

## Motivation

`coding.agents`, `providers`, and `models` are read once, in `NewApp` at process boot: adapter construction, executable resolution, and the runner's environment-variable allowlist all happen there. Unlike `repositories`, which live in `state.json` and take effect immediately through an approval flow, these sections cannot be made to hot-reload without touching the runner's sandbox-boundary wiring — so a restart stays required. The gap this closes is purely the "how do I safely write the YAML" step, which today means an interactive SSH session and hand-typing YAML into a heredoc.

## Scope

In scope: `coding.agents.*`, `providers.*`, `models.*` (Version 2 `ModelAliases`). These are the sections an operator plausibly needs to extend after first boot.

Out of scope, deliberately:
- `/config remove` — no removal command in this iteration.
- Changing `coding.default_agent` or `agent.default_model` themselves — already covered by the existing `/coding_agent default` / `/model default` commands against the *runtime* selection persisted in `state.json`.
- Any field outside the three whitelisted sections (`telegram.owner_id`, `server.*`, `calendar.*`, `runner.*`, etc.) — a bad value there can lock the owner out of the bot or break the webhook, and none of them plausibly need runtime additions the way agent/provider/model registries do.

`/config set` requires a Version 2 config file. `legacyConfigDocument.MarshalYAML` has no `Coding` field and no `Providers`/`ModelAliases` maps at all — a Version 1 file uses hardcoded `models.flash`/`models.pro` IDs, and writing a coding-agent mutation back to it would silently vanish on the next marshal. `/config set` on a Version 1 config returns an explicit error telling the owner to migrate to version 2 first; it does not attempt a partial or lossy write.

## Commands

Owner-only, dispatched through the existing `CommandService.Execute` switch in `internal/bootstrap/commands.go` — this makes them available on Telegram and via `eggy config ...` (the CLI already forwards its argument list as `/` + joined fields to the same dispatcher, per `cmd/eggy/main.go`).

```text
/config get coding
/config get providers
/config get models

/config set coding_agent <alias> <adapter> [credential_env]
/config set provider <name> <adapter> <base_url> <api_key_env>
/config set model <alias> <provider> <model_id>
```

`get` lists the current entries in each section: alias/name, adapter, and (where applicable) the configured environment-variable *name* for the credential — never a secret value, since `Config` only ever stores env var names in these sections, never the credential itself.

`set` is a typed command per entity — not a generic dotted-path setter. Each variant validates only the fields relevant to that entity and produces a specific error (e.g. `"adapter must be codex_cli or claude_cli"`), the same way `/repositories add` and existing config validation already report errors. Re-registering an existing alias/name overwrites that entry; this is add-or-replace, not create-only.

## Write path

New file `internal/bootstrap/config_mutate.go`, alongside the existing `config.go` and `config_init.go`:

1. Read `config.yaml` fresh from disk (not the in-memory `Config` the running process booted with) so concurrent writers — the CLI and a live daemon, or two CLI invocations — don't clobber each other's changes.
2. Decode it exactly as `LoadConfig` does today (`decodeKnownYAML` into `configV2Document` / `legacyConfigDocument`, then `normalize*Config`), on a copy. If the file's `version` header is `1`, reject immediately with a "migrate to version 2 first" error before any mutation is attempted.
3. Apply the one typed mutation from the command (e.g. `cfg.Coding.Agents[alias] = CodingAgentConfig{Adapter: adapter, CredentialEnv: credentialEnv}`).
4. Run the existing `cfg.Validate()` — the same structural checks already enforced at boot (`validateCoding` / `validateProviders`: adapter enum, alias/name pattern, `default_agent`/`default_model` cross-references, provider→model references). On failure, reply with that exact error and leave the file untouched.
5. On success, atomic write: temp file in the same directory as `config.yaml`, mode `0600`, `Write`, `Sync`, `Close`, then `os.Rename` over the original — the identical sequence `initializeConfig` already uses in `config_init.go`. Steps 1–5 as a whole run inside `filelock.With(path, ...)`, not just the final write — the lock must cover the read too, or two concurrent `/config set` calls can both read the old file, mutate their own copy, and the second write silently discards the first writer's change.
6. Reply confirming what was written and that a restart is required for it to take effect (matching how `CLAUDE_CODE_OAUTH_TOKEN` is documented in the README today). This step deliberately does **not** run `validateSecrets()` — credential presence is a boot-time concern, and a missing credential will already surface as a clear, actionable error in Railway's deploy logs when the process restarts, exactly as it does today for a misconfigured default agent.

Marshaling reuses `Config.MarshalYAML()`, which already exists and is exercised by `config_init_test.go` — no new serialization logic.

## Data flow

```text
Telegram/CLI  ->  CommandService.Execute("/config set ...")
              ->  config_mutate.go: load config.yaml, apply mutation to a copy
              ->  Config.Validate() (existing, unchanged)
              ->  valid?  -> filelock + atomic write -> confirm + restart reminder
                  invalid? -> discard copy, return validation error, file untouched
```

No new component touches the running `App`, the runner, or `state.json`. The daemon's in-memory config is unaffected until the operator restarts it.

## Security

- Owner-only, same authorization boundary as every other command in `CommandService` — Telegram authorization happens at the webhook layer before dispatch; the CLI has no separate gate because holding CLI+config access is already an equivalent trust level to SSH access, which this feature exists to avoid needing.
- Whitelist keeps identity- and transport-critical fields (`telegram.owner_id`, `server.*`, `calendar.*`) unreachable from chat, so a typo can't lock the owner out of the bot or break the Telegram webhook.
- No secret values ever pass through `/config get` or appear in a `/config set` confirmation — only adapter names, URLs, and environment-variable *names*, matching the existing invariant that credentials never enter prompts, state, or command output.
- Atomic write + `filelock` prevents a torn or concurrently-corrupted `config.yaml`.

## Testing

Test-first, per repository convention:
- Failing tests first for: `/config get` output for each section, `/config set coding_agent` add and overwrite, `/config set provider`, `/config set model`, rejection of an invalid adapter/alias/URL/env-var-name leaving the file unchanged, `default_agent`/`default_model` cross-reference rejection when the referenced entry doesn't exist, and rejection of `/config set` against a Version 1 config file.
- A concurrent-write test reusing the `filelock` pattern already covered in `config_init_test.go`.
- A round-trip test: write via `/config set`, then reload with `LoadConfig` and confirm the new entry is present and the rest of the file is unchanged.

Required completion checks, unchanged:

```text
make fmt vet test race build
```
