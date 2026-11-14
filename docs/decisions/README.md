# Recorded design decisions

Arguments that were had once, kept so they are not re-had by accident. They
record why things are as they are, not rules: reopen one by saying what has
changed since. Security decisions are not here; they stay in `AGENTS.md`.

## Design decisions

**The two OAuth flows stay separate.** Unifying `internal/google/oauth.go`
and `internal/mcp/oauth.go` behind one parameterized adapter was considered
and rejected. Shared: state and verifier generation, the pending window check,
the exchange, token persistence — 50-60 lines. Not shared, and structural, all on
the MCP side: protected-resource discovery, authorization-server metadata with
trailing-slash tolerance and synthesized fallback endpoints, RFC 7591 dynamic
registration, a `resource` parameter threaded through every call, per-server
keying, and conformance to the MCP SDK's `auth.OAuthHandler`. Google has fixed
endpoints and one loopback constant. One adapter would carry all of that past a
provider that never uses it, adding branches to delete ~50 lines. Revisit only if
a *third* provider appears on the MCP side of the split.

**`GoogleRuntime` and `MCPRuntime` stay separate.** Four lines each; collapsing
them needs a server key Google ignores or a named-grant registry. Machinery to
delete eight lines.

**`parseOAuthRedirect` stays in `internal/commands`.** Its header calls itself
homeless, but both callers *are* the command surface, and moving it would have
two extension packages import a third.

**Tool registration stays inline in `NewApp`.** Extracting a `buildToolRegistry`
helper was tried and rejected: it needs eleven collaborators, so it trades a long
function for an eleven-parameter one and hides the coupling instead of removing
it. It becomes worth revisiting only if what a turn's tool set is assembled from
gets smaller.

**Adding a model backend is usually config, not code.** `openai_compatible` is
OpenAI's Chat Completions wire format — `/chat/completions`, `tools` /
`tool_calls`, `reasoning_effort`, `usage.cached_tokens` — so OpenAI itself,
DeepSeek, OpenRouter, and Groq are all reachable by adding a `providers` entry
with the right `base_url` and `api_key_env`. Writing an `openai` adapter that
duplicates `openaicompat` is the wrong turn, and a test pins that. A new adapter
is earned by a genuinely different wire format: Anthropic's Messages API, with
its top-level system prompt, content blocks, and required `max_tokens`, is the
real example.

**Config field count is not a cleanup target.** The fields are not redundant:
~20 are MCP server options (transport, auth, OAuth client, timeouts, failure
policy, tool filters), 7 are Google, and the rest are server, runner, and
repository settings that each do something. None is a second way to do a job.
Cutting the count means removing a capability or removing knobs from MCP — and
MCP's knobs are what let capability arrive as configuration instead of code,
which is the property the whole design is built on.

**Production line-count targets are retired.** A `≤ 12,500 production lines`
target counted the tree rather than what a turn costs to run, and it moved away:
successive cleanup passes each netted slightly larger, because moving code where
it belongs costs naming and comments. It was not reachable without deleting a
capability, which is a product decision to argue on its own merits rather than
smuggle in as a line-count exercise. Deleting genuinely dead code still counts;
shrinking to hit a number does not.

**`enabled` flags stay on MCP servers and Google.** The rule "an empty section is
the off switch" does not hold when the section carries state that is expensive to
recreate. An MCP server entry holds a URL, auth mode, OAuth client id, timeouts,
and tool filters; Google holds a client id and product list. `enabled: false`
turns the capability off while keeping all of it, which deleting the section does
not. The MCP manager also reads `Enabled` at runtime to gate reconnection, so the
field is load-bearing rather than declarative. Removing it would break every
existing config (`KnownFields(true)` rejects the now-unknown key) and cost
migration code to fix.

**The unknown-key message is already a repair.** `KnownFields(true)` reports the
line, the key, and the section type: `line 34: field enabld not found in type
config.GoogleConfig`. Adding a "did you mean" suggestion needs edit distance over
yaml tags plus mapping a type name in an error string back to a `reflect.Type` —
real machinery for a message that already names the file position and the exact
key.

**`config.example.yaml` stays a starter, not a reference.** Asserting it
documents every field would force the advanced MCP options, tool filters, and
failure policy into a file whose job is to get someone running. Field reference
belongs on the docs site. `TestLoadConfigAcceptsExample` already pins that the
example parses and produces the expected values, which is the property worth
holding.

**`knownGoogleProducts` stays duplicated in `internal/config`.** The boundary
rule forces it: config may not import an extension package. If a third copy appears,
invert it — let the adapter validate its own product names and have config check
only the shape.

## Capabilities declined while Eggy was one server for a few people

Moved here on 2026-11-14, when `AGENTS.md` reopened them. The reasoning is kept
as it was written.

Each is something a comparable project (OpenClaw, Hermes Agent) has and Eggy will
not build. Recorded with the reason so re-reading their READMEs does not restart
the argument.

**Channel breadth.** Both front 10-20+ messaging platforms. Eggy's people are
reachable on Telegram and on the web. A tenth channel does not let anyone do
anything an existing one does not — it multiplies adapters, webhook
authentication, deduplication ledgers, and approval-delivery paths for zero new
capability. `ports.Channel` stays the extension point if that ever changes;
adding a channel should remain an extension package plus one line of bootstrap, and
that property is worth more than any particular channel.

**Multi-agent routing.** OpenClaw routes to several agents per workspace
because it serves groups. Eggy has one agent, one loop, and one scheduler for
everyone it serves; what changed with accounts (below) is who owns a record,
not how many agents there are.

**Subagent delegation (`delegate_task`).** Hermes spawns isolated subagents with
spawn-depth and concurrency bounds, an orchestrator/leaf role split, and an
async-completion queue re-entering the parent conversation. That is an agent
framework, which "Do not introduce" forbids by name, and it needs a second loop,
which the boundary rules forbid by name. It is also the single largest net-new-
code item either reference has. If parallelism is ever needed, the answer is a
scheduled turn, not a child loop.

**Runtime-loaded plugins.** Both discover plugins from directories and entry
points at runtime. Go's build model makes it worse than it is for Python or Node.
Compile-time wiring in `internal/bootstrap` plus MCP for out-of-tree capability
covers the same ground.

**A TUI, a desktop app, or companion nodes.** Each is a whole client to maintain.
Eggy's surfaces are a phone the owner already has and a browser, and production
stays a single `eggyd` process.

**Pluggable memory providers.** Hermes plugs several in behind an ABC — and its
own policy now refuses new in-tree ones, because the maintenance burden is what a
provider interface actually buys. Eggy has three durable forms on purpose — YAML
for startup config, Markdown for owner-facing documents, SQLite for everything
machine-managed. A memory provider port would be a second way to do a job that
SQLite and Markdown already do.

**Profiles / multi-instance isolation.** Eggy has one home, one process, one
replica; `EGGY_CONFIG` already relocates it for tests and local runs. Several
people share that one instance as accounts (below) rather than each running
their own.
