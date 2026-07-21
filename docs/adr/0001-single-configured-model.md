# ADR 0001: One configured model, no automatic Flash/Pro escalation

## Context

Eggy's original MVP spec split assistant work across two DeepSeek models:
V4 Flash handled ordinary turns, and a deterministic policy silently
escalated to V4 Pro after a tool-step threshold, repeated recoverable tool
failures, or a detected "complex" non-coding request (at most once per
turn). This added a second model configuration surface, a hidden routing
decision the owner couldn't fully predict from Telegram, and coupled Eggy's
configuration format to one provider's two-tier model lineup.

## Decision

Configuration version 2 introduces named providers and named model aliases
behind the existing `ports.Model` interface, with one `agent.default_model`
alias used for every turn. The owner can inspect and change the active alias
with `/model`; there is no automatic escalation, no silent provider failover,
and a transient failure is retried a bounded number of times on the same
alias before Eggy reports it. Version 1 configuration files still load,
mapped to a single implicit alias, but the escalation behavior itself is
gone — it is not preserved as a compatibility path.

## Consequences

Model selection is a single, owner-visible fact instead of a hidden runtime
decision. Adding another OpenAI-compatible provider or model is a
configuration change, not a kernel change. The cost is that Eggy no longer
automatically reaches for a stronger model on a hard request — the owner
must choose a more capable alias explicitly if a task warrants it.
