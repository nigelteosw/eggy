# ADR 0002: Native Go implementation loop, not a delegated coding-agent CLI

## Context

Eggy originally split repository work across two engines: the selected
reasoning model answered conversation and read-only questions through
hand-rolled Go tools, while any actual edit was handed to a detached Codex
CLI subprocess (later, optionally, a Claude Code CLI subprocess selected
through a `/coding_agent` alias) with its own internal tool loop, its own
context window, and its own subscription-billed authentication
(`CODEX_HOME` device-login, `CLAUDE_CODE_OAUTH_TOKEN`). Eggy's own result
contract depended on parsing the last line of the subprocess's stdout as
JSON, with a markdown-fence-stripping fallback for when it didn't comply.

## Decision

The selected model is the only execution engine, for both conversation and
repository edits, using real tools (`read_file`, `terminal`, `patch`,
`write_file`, `finish_implementation`) inside a second, bounded instance of
Eggy's own `agent.Loop` — the same tool-calling engine every other turn
already runs through. Codex CLI and Claude Code CLI, their adapters, and
their alias-selection plumbing (`/coding_agent`, `coding.agents`,
`coding.default_agent`) are removed from the codebase. `finish_implementation`
replaces stdout-JSON-scraping with an actual typed terminal tool call the
loop enforces directly.

Eggy keeps its own clone → branch → diff → independent approval (commit →
push → pull request) pipeline unchanged; neither reference architecture this
design drew on has an equivalent, because neither runs unsupervised against
real repositories with real push/PR credentials on a single owner's behalf.
That gate is Eggy's responsibility to hold, not something a coding-agent CLI
was ever going to provide.

## Consequences

One engine, one context model, one authentication story — no second
subscription-billed CLI to install, authenticate, or keep in version lockstep
with Eggy's own container image. The trade-off is real: a general reasoning
model doing its own multi-file editing and validation, unsupervised inside
the sandbox up to a step budget, is asked to do more than before, and should
be expected to need more `patch`/`terminal` round trips to reach a working
diff than a coding-tuned CLI needed. The existing approval gate is the
backstop this trade-off leans on — nothing lands without it.
