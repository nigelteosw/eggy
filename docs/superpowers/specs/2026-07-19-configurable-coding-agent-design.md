# Configurable Coding Agent Design

## Goal

Eggy can use either Codex CLI or Claude Code for repository inspection and modification. The owner selects the active coding-agent alias at runtime with Telegram, and the selection persists across restarts. Codex remains the default so existing deployments keep their current behavior.

## Configuration

Version 2 configuration gains a provider-neutral coding-agent registry:

```yaml
coding:
  default_agent: codex
  agents:
    codex:
      adapter: codex_cli
    claude:
      adapter: claude_cli
      credential_env: CLAUDE_CODE_OAUTH_TOKEN
```

Agent names are operator-defined aliases. Bootstrap validates alias syntax, supported adapter names, credential environment-variable names, and that `default_agent` references a configured entry. Version 1 configuration normalizes to a single `codex` alias. Existing version 2 files that omit `coding` receive the same Codex-only default, preserving compatibility.

Credentials remain outside YAML. Bootstrap reads each configured `credential_env` into an adapter-scoped secret map. An absent optional credential makes that alias unavailable at runtime; it does not prevent a Codex-only deployment from starting unless the unavailable alias is configured as the default.

## Runtime selection

The provider-neutral `CodingAgentRuntime` owns:

- the configured alias-to-`ports.CodingAgent` map;
- the default alias;
- the persisted selected alias; and
- the alias assigned to each active run so interruption still reaches the correct adapter if the owner changes the global selection mid-run.

`ports.State` gains an additive `coding.selected_agent` field. An empty value means the configured default, so existing `/data/state.json` files require no schema migration or version change.

The owner command is:

```text
/coding_agent
/coding_agent codex
/coding_agent claude
/coding_agent default
```

The bare command reports the active and available aliases. Selection is global because Eggy is a single-user service. Unknown or unavailable aliases are rejected without changing state. The command name uses an underscore because Telegram bot command names do not permit hyphens.

## Adapter isolation

`internal/kernel` and `internal/ports` remain provider-neutral. They do not import or name Codex or Claude packages. `internal/bootstrap` constructs the configured adapters and registers them with the runtime.

The Codex adapter moves ownership of `CODEX_HOME` out of `CodingService`. The Claude adapter owns `CLAUDE_CODE_OAUTH_TOKEN` and `CLAUDE_CONFIG_DIR`. `CodingService` passes only the provider-neutral coding request; credentials are injected by the selected adapter and never enter prompts, state, progress messages, diffs, or errors.

## Claude Code execution

The Claude adapter invokes Claude Code non-interactively inside the existing restricted runner workspace:

```text
claude -p --output-format stream-json --verbose --permission-mode bypassPermissions <instruction>
```

Eggy's runner still enforces the workspace root, sanitized environment, timeout, output cap, and process-group termination. `bypassPermissions` prevents a headless Claude process from blocking on terminal approval prompts; it does not bypass Eggy's independent commit, push, pull-request, or Calendar approval services. This remains suitable only for configured trusted repositories, matching Eggy's existing same-container Codex boundary.

Claude receives the same runner contract and root `AGENTS.md` guidance as Codex. The prompt requires the final assistant result to be the existing `ports.CodingResult` JSON shape. The adapter parses newline-delimited events into bounded progress updates, extracts the final result, rejects malformed or incomplete structured output, and supports cancellation by run ID.

Read-only inspection uses Claude's `plan` permission mode and the existing post-run branch, HEAD, and diff verification. Modification uses `bypassPermissions`, after which Eggy independently verifies that Claude did not change the branch or HEAD before presenting the diff for approval.

## Packaging and authentication

The production image pins both CLI versions. It creates `/data/codex` and `/data/claude`, sets their provider-specific config directories, and keeps the existing Railway volume at `/data`.

For Claude subscription authentication, the operator runs `claude setup-token` on a trusted local machine and stores the printed one-year token as the Railway secret `CLAUDE_CODE_OAUTH_TOKEN`. Eggy forwards it only to the Claude subprocess. The token is never copied into config, state, or the volume.

## Failure behavior

- Selecting an unknown alias returns the configured aliases.
- Selecting a configured alias whose executable or credential is unavailable returns an explicit unavailable error and preserves the previous selection.
- A selected alias that becomes unavailable after startup fails the requested coding run honestly; Eggy does not silently switch providers.
- Malformed Claude stream events produce bounded diagnostics; a missing or invalid final result fails the run.
- Authentication failures are redacted and reported as Claude Code failures without exposing the token.
- Changing selection affects new runs only. Existing runs retain their starting adapter for progress and interruption.

## Testing and verification

Tests cover configuration compatibility and validation, persisted global selection, concurrent state updates, active-run interruption routing, command output, adapter environment isolation, Claude command modes, stream normalization, malformed results, and Docker manifest changes. Adapter tests use fake runners and fake CLI output; the default suite makes no live Anthropic requests.

The required completion checks remain:

```text
make fmt vet test race build
```

Docker smoke runs only when Docker is available. No test logs or fixtures contain the real Railway token.
