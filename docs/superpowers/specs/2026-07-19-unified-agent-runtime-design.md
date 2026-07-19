# Eggy Unified Agent Runtime Design

## Goal

Eggy should behave like a coherent personal agent rather than a collection of independently routed chat and coding paths. Telegram is a channel into one provider-neutral, tool-capable agent runtime. DeepSeek Pro is the default reasoning model, configured through an OpenAI-compatible provider alias, while Codex remains the dedicated repository inspection and modification engine.

The design takes high-level guidance from OpenClaw, NanoClaw, Hermes Agent, and the structure of the user-provided GPT steering reference. Eggy will adopt the useful patterns—one agent loop, explicit capability context, layered instructions, typed tools, truthful status, context files, and deterministic security boundaries—without copying provider-specific prompt text or expanding into a general agent framework.

## Product behavior

Deterministic slash commands continue to bypass the model. Every other owner message enters the same agent loop with the selected model alias, available tools, current runtime capabilities, durable context, and recent conversation state.

The target flow is:

```text
Telegram update
  -> owner and webhook verification
  -> deterministic slash command, when matched
  -> otherwise unified agent loop
       -> selected OpenAI-compatible model alias
       -> typed tool calls
       -> verified tool results returned to the model
       -> grounded Telegram response
```

The keyword-based `CodingIntent` split is removed from normal conversation. Repository behavior is exposed through explicit tools, so the model can list configured repositories, inspect one read-only, or start a modifying Codex run according to the user's intent.

## Provider and model configuration

Configuration version 2 introduces named providers, named model aliases, and an agent default:

```yaml
version: 2
agent:
  default_model: "deepseek-pro"
providers:
  deepseek:
    adapter: "openai_compatible"
    base_url: "https://api.deepseek.com"
    api_key_env: "DEEPSEEK_API_KEY"
models:
  deepseek-pro:
    provider: "deepseek"
    model: "deepseek-v4-pro"
```

Provider names and model aliases are operator-controlled identifiers. A provider contains a supported adapter name, an HTTP(S) base URL, and the name of the environment variable holding its API key. A model alias references one configured provider and one provider-native model identifier. Secret values never enter YAML.

The first release supports OpenAI-compatible chat-completions providers only. The provider-neutral `internal/ports.Model` boundary remains unchanged in purpose; OpenAI-compatible request, response, authorization, usage, and error types stay inside the model adapter. Provider and alias registration happens only through `internal/bootstrap`.

DeepSeek Pro is the first-boot default and is used for every agent turn. Eggy removes the flash-first heuristic and automatic flash-to-Pro escalation. There is no silent provider failover. A transient request may be retried using the same alias within a fixed bound, after which Eggy reports the failure.

The owner can inspect and change the active alias through deterministic commands:

```text
/model
/model deepseek-pro
/model default
```

Only configured aliases are accepted. The selected alias is global to Eggy's single-owner runtime, persists across restarts, and survives `/new`. `/model default` clears the override and returns to `agent.default_model`.

## Version 1 configuration compatibility

Existing version 1 files remain loadable and are never rewritten automatically. Bootstrap maps the legacy DeepSeek flash/Pro configuration into an implicit OpenAI-compatible `deepseek-pro` alias in memory, using the legacy Pro model identifier and `DEEPSEEK_API_KEY`. The legacy flash model and escalation thresholds are accepted for compatibility but are not used by the unified runtime.

New installations generate version 2 configuration. Operators who want multiple providers can deliberately edit the persisted YAML to version 2. An invalid provider reference, duplicate alias, unsupported adapter, malformed URL, unsafe environment variable name, or missing active-provider credential prevents startup with a precise, secret-free error.

## Instruction and context hierarchy

Each agent turn assembles instructions in a fixed order:

1. **Hard runtime policy.** Provider-neutral rules compiled into Eggy. Current user instructions override memory; credentials must never be requested, displayed, placed in tool arguments unnecessarily, or stored; tool success must not be claimed without a successful result; protected actions must use their approval workflow; unavailable capabilities must be reported truthfully.
2. **Generated capability manifest.** Safe runtime facts built from actual bootstrap state: selected model alias, configured repository names, registered tool names and limits, Codex readiness, and enabled optional integrations. It contains no credential values.
3. **`SOUL.md`.** Operator-owned identity, tone, priorities, and working style. The agent can read but cannot edit it. It cannot override hard policy.
4. **`USER.md`.** Agent-curated user profile and durable preferences.
5. **`MEMORY.md`.** Agent-curated durable facts and lessons, explicitly treated as potentially stale context rather than current instructions.
6. **Conversation context.** The existing summary and recent messages.
7. **Current tool calls and results.** Evidence for the current turn.

This hierarchy adapts the useful steering ideas in the referenced prompt: explicit trust order, factuality, capability awareness, evidence-backed action claims, clear tool requirements, uncertainty disclosure, and scoped autonomy. Product-specific instructions from the reference are not copied into Eggy.

## Context file lifecycle

On first start, Eggy creates missing context files in `data_dir`:

```text
SOUL.md
USER.md
MEMORY.md
```

Files are created atomically with mode `0600` and are never overwritten when present. Existing `MEMORY.md` content remains compatible. Seeded `SOUL.md` describes Eggy as a practical personal engineering assistant but leaves security and approval rules in the compiled hard policy. `USER.md` begins with a minimal profile structure, and `MEMORY.md` keeps its existing section-based format.

Context loads have explicit size limits so a malformed or unbounded file cannot consume the model context window. A missing file is recreated; an unreadable, oversized, or structurally invalid file produces a clear readiness/startup error rather than being silently ignored.

The agent can curate `USER.md` and `MEMORY.md` through narrow tools. It cannot edit `SOUL.md`. Writes remain section-based, atomic, and locked. Tool descriptions tell the model to retain stable preferences, project facts, and reusable lessons, not transient chat or unsupported assumptions.

Before a profile or memory write reaches disk, a provider-neutral secret guard rejects likely passwords, bearer tokens, API keys, private keys, authorization headers, credential-oriented sections, and any exact active secret value. This is defense in depth rather than a claim of perfect secret classification. The hard policy also tells the model never to solicit secrets in Telegram and to direct the owner to Railway variables or another external credential store.

## Repository tools and Codex delegation

The unified loop receives three repository capabilities:

### `repository_list`

Returns repository names and safe metadata from the runtime registry. It never infers repositories from memory. With no configured repository, it returns a structured `not_configured` result containing safe setup guidance.

### `repository_inspect`

Accepts a configured repository name and a read-only question. The repository service creates an isolated checkout of the configured base branch, discovers root `AGENTS.md`, and delegates inspection to Codex in read-only mode. The adapter returns a bounded textual answer and validation metadata. The service verifies that the checkout has no diff before reporting success and then removes the temporary workspace.

Inspection does not create a feature branch, persist repository changes, or request an approval. It is appropriate for questions such as “what framework does Eggy use?” or “where is webhook authentication implemented?” GitHub credentials remain in the repository adapter's restricted environment and never enter the Codex prompt or model conversation.

### `repository_modify`

Accepts a configured repository name and an explicit change request. It uses the existing isolated Codex coding flow: clone the trusted repository, create an `eggy/<run-id>` branch, load `AGENTS.md`, run validation, capture the bounded diff, and request commit approval. Commit, push, and pull-request creation remain independent approval steps. Protected branches remain unpushable even with approval, and Eggy never merges.

Repository services remain provider-neutral. GitHub and Codex request types, credentials, commands, and response parsing stay inside their adapters, with registration through bootstrap.

## Tool behavior and truthful responses

The model sees only tools actually registered for the current deployment. Tool definitions state preconditions, side effects, approval requirements, and meaningful failure codes. Unknown tool calls, invalid schemas, step-limit exhaustion, and missing capabilities are handled deterministically.

The agent may reason about a task before selecting a tool, but it cannot obtain repository contents from conversational memory. Answers about repository implementation must follow a successful inspection result or clearly state that inspection was unavailable. Claims that a memory write, schedule mutation, Calendar mutation, coding run, commit, push, or pull request succeeded must follow the corresponding successful tool result.

Calendar mutations and repository shipping retain independent approval enforcement below the model layer. Prompt text can guide behavior but cannot bypass these checks.

## Usage accounting

The OpenAI-compatible adapter parses provider-supplied usage fields without fabricating unavailable measurements:

- prompt tokens;
- completion tokens;
- total tokens;
- cached prompt tokens, when supplied;
- reasoning tokens, when supplied.

Each successful model response records usage by model alias. Tool execution itself does not create model usage unless another model turn follows. Deterministic commands such as `/status` do not consume model tokens.

The owner can inspect and reset locally recorded counters:

```text
/usage
/usage reset
```

The response distinguishes local provider-reported totals from provider billing dashboards and includes the active alias. Resetting local counters does not affect provider billing records.

## State migration

Persisting the selected model and usage counters requires `/data/state.json` schema version 2. The migration from version 1 adds optional agent-runtime state while preserving processed events, tasks, approvals, coding runs, schedules, conversation summary, recent messages, Calendar enrollment, and all existing identifiers.

State migration follows the existing compare-and-update discipline and writes atomically. Tests load a real version 1 fixture, migrate it, reload it, and compare every legacy field. Unknown future schema versions remain rejected. Migration failure leaves the original file recoverable and produces a startup error.

## Error handling and observability

Provider errors are normalized into safe categories such as authentication, rate limit, timeout, unavailable, invalid request, and malformed response. Response bodies, authorization headers, configured secret values, and credential-bearing URLs are redacted before an error reaches logs, state, the agent, or Telegram.

Tool failures return structured, bounded results to the agent. The model may correct a recoverable input problem within the existing tool-step limit. Exhausted retries, unavailable Codex authentication, clone failure, inspection mutation, approval rejection, and provider failure are reported honestly instead of triggering a fabricated success or silent provider switch.

Safe startup logs include the active model alias, provider name, configured repository names, enabled integrations, and context file paths. They do not include API keys, token fragments, authorization values, or environment contents.

Readiness verifies that configuration, state, context files, and required local adapters are usable. It does not make a billable provider request. `/status` remains a local operational view.

## Acceptance behavior

The transcript that exposed the current defect becomes an end-to-end test:

- “What repositories can you work on?” calls `repository_list` and reports only runtime-configured repositories.
- “GitHub repo is `nigelteosw/eggy`” does not claim configuration changed and explains that repository configuration is operator-managed.
- “Tell me what framework it uses” calls `repository_inspect` and grounds the answer in the Codex result.
- “Don't you have my GitHub token?” explains that credentials are externally managed and invisible to the model. It never requests the token or offers to store it.
- An autonomous memory decision may retain a stable repository preference, but any credential-like write is rejected.
- `/model` lists aliases, changes the active alias, and survives restart.
- `/usage` increases after an ordinary DeepSeek-backed turn but not after `/status`.
- Read-only inspection leaves no diff or branch.
- A modification cannot commit, push, or create a pull request without the existing separate approvals.

## Testing strategy

Behavior changes are implemented test-first with a focused failing test before each production change.

Bootstrap and configuration tests cover version 1 compatibility, version 2 validation, first-boot generation, environment-backed provider secrets, provider/model registration, context-file creation, never-overwrite behavior, capability-manifest accuracy, and safe startup errors.

Kernel tests cover instruction ordering, selected-model state, usage aggregation and reset, typed tool routing, tool-step limits, truthful missing-capability results, autonomous memory tools, secret rejection, repository inspection versus modification, and approval preservation.

Adapter tests cover OpenAI-compatible request translation, authorization isolation, provider model identifiers, tool calls, all supported usage fields, retry classification, error redaction, and malformed responses. Codex inspection tests prove bounded output and a clean checkout; modification tests preserve the existing shipping approval chain.

Integration tests use fake provider, Telegram, repository, and Codex adapters to reproduce the acceptance transcript without network access or real credentials. Migration fixtures prove exact legacy state preservation. Race tests exercise concurrent state, context, and usage updates.

Before completion, the repository must pass:

```sh
make fmt vet test race build
make smoke
```

Docker smoke runs when a Docker daemon is available. It starts from an empty volume, verifies version 2 first boot and context files, exercises health/readiness, and confirms that no secret appears in persisted configuration or context.

## Scope limits

This change does not add a web framework, ORM, database, agent framework, native plugin runtime, multi-user routing, arbitrary provider plugins, web browsing, subagents, or automatic provider failover. OpenAI-compatible aliases are the only new provider family. Telegram remains owner-only, configured repositories remain trusted, and the existing process, environment, timeout, output, and approval restrictions remain intact.

## References

- OpenClaw: <https://github.com/openclaw/openclaw>
- NanoClaw: <https://github.com/qwibitai/nanoclaw>
- Hermes Agent: <https://github.com/NousResearch/hermes-agent>
- User-provided steering reference: <https://github.com/asgeirtj/system_prompts_leaks/blob/main/OpenAI/gpt-5.6-sol-extra-high.md>
