# Eggy MCP client design

**Status:** Approved for implementation planning
**Date:** 2026-07-22

## Context

Eggy's outer agent loop already consumes provider-neutral `ports.Tool`
implementations through a registry assembled in `internal/bootstrap`. Adding an
MCP client therefore does not require a second agent loop, a plugin runtime, or
new kernel abstractions. An MCP server is another external tool provider: the
MCP adapter discovers its tools, projects them into `ports.Tool`, and lets the
existing loop execute them.

The design follows the established MCP-host pattern used by Hermes Agent and
OpenClaw: a configured server registry, SDK-managed connections and OAuth,
tool discovery, per-server filtering, namespaced proxy tools, isolated failure
handling, and conventional management commands. Pi's relevant principle is to
keep MCP outside the minimal agent core rather than building protocol behavior
into the loop.

Railway's hosted MCP server at `https://mcp.railway.com` is the first real
integration. The adapter remains generic so later remote MCP servers can be
added through configuration.

## Goals

- Let Eggy's outer agent loop call tools from multiple remote MCP servers.
- Use the official Go MCP SDK for protocol, transport, OAuth, discovery,
  notifications, and tool calls.
- Keep `internal/kernel` and `internal/ports` provider-neutral and unchanged.
- Register MCP tools only through `internal/bootstrap`.
- Reuse Eggy's existing source-based tool availability: direct owner turns may
  receive MCP tools, while heartbeat, scheduled, and implementation turns do
  not.
- Make one server's authentication or availability failure non-fatal to Eggy
  and other MCP servers.
- Preserve `/data/state.json` compatibility by storing MCP OAuth state in
  separate encrypted files.
- Make adding a conventional remote MCP server a configuration operation, not
  a kernel modification.

## Non-goals

- Running Eggy as an MCP server.
- Loading executable plugins or provider code at runtime.
- Supporting stdio or legacy SSE transports in the first version.
- MCP resources, prompts, roots, sampling, elicitation, or MCP Apps.
- Adding a second MCP-specific permission or approval framework.
- Making MCP tools available inside repository implementation runs.
- Replacing existing Calendar, repository, shipping, or scheduling adapters.
- Automatically installing MCP servers, CLIs, packages, or manifests.

## External precedents

- Hermes discovers tools from configured MCP servers and registers proxy tools
  in its normal tool registry. It supports per-server timeouts, filtering,
  reconnect behavior, and isolated failures.
- OpenClaw keeps an `mcp.servers` registry with enablement, transport, auth,
  connection/request timeouts, tool include/exclude filters, login/logout,
  probe, reload, cached runtimes, and explicit catalog reload after list
  changes.
- Pi keeps MCP out of its minimal agent core and expects an extension boundary
  to translate MCP tools into the host's native tool interface.

Eggy adopts these mechanics in Go. It does not create an Eggy-specific MCP
protocol or orchestration layer.

## Architecture

The new adapter package is:

```text
internal/adapters/tools/mcp/
```

It owns all MCP SDK types, wire behavior, transports, OAuth handlers,
credential persistence, catalog discovery, name normalization, result
conversion, and connection lifecycle. Provider request and response types do
not escape the package.

The data flow is:

```text
bootstrap MCP configuration
        |
        v
generic MCP manager
        |
        +-- one runtime per enabled server
        |      |
        |      +-- SDK Streamable HTTP session
        |      +-- discovered and filtered catalog
        |      +-- namespaced ports.Tool proxies
        |
        v
existing services.ToolRegistry
        |
        v
existing outer agent.Loop
```

The adapter returns `[]ports.Tool` plus bounded server diagnostics.
`internal/bootstrap` registers those tools alongside Eggy's native tools. No
MCP tool self-registers and no adapter imports bootstrap.

The manager is also an `io.Closer`-style lifecycle owner. Eggy closes it during
shutdown so active requests are cancelled and every SDK session is closed.

## Configuration

Bootstrap configuration gains a map of named MCP servers:

```yaml
mcp:
  servers:
    railway:
      url: https://mcp.railway.com
      transport: streamable-http
      auth: oauth
      enabled: true
      connect_timeout: 10s
      timeout: 60s
      max_output_bytes: 131072
      supports_parallel_tool_calls: false
      tool_filter:
        include:
          - check-railway-status
          - list-projects
          - create-project-and-link
          - list-services
          - link-service
          - deploy
          - deploy-template
          - create-environment
          - link-environment
          - set-variables
          - generate-domain
          - get-logs
        exclude: []
```

Server names must be unique, non-empty slugs. URLs must use HTTPS. The only
first-version transport value is `streamable-http`.

Supported authentication modes are the standard remote-host forms:

- `oauth`: MCP authorization-code flow managed through the SDK.
- `bearer-env`: an authorization token read from a named environment variable.
- `none`: no authorization header, allowed only for an explicitly configured
  HTTPS endpoint.

`bearer-env` requires a sibling `bearer_token_env` setting containing the
environment-variable name. `oauth` may specify an `oauth_scopes` list when the
server documents explicit scopes; otherwise the SDK follows authorization
metadata and challenge responses. An OAuth-enabled server also requires
Eggy's existing encryption key so its credentials can be persisted safely.

Literal bearer tokens and arbitrary secret header values are not accepted in
YAML. Secrets come from environment variables or encrypted OAuth storage.

`tool_filter.include` and `tool_filter.exclude` contain remote MCP tool names,
not projected Eggy names. They support exact names in v1; glob syntax can be
added later if a demonstrated server catalog needs it. If `include` is empty,
all discovered tools are candidates. Exclusions win. A configured include name
that the server does not advertise is reported as a warning by `/mcp status`
and `/mcp probe`.

`list-variables` is intentionally omitted from Railway's initial include list
because it can place environment secrets directly into model context. This is
a Railway configuration decision, not behavior hardcoded into the generic
adapter.

## Authentication and credential storage

The official Go MCP SDK owns OAuth discovery, PKCE, dynamic client
registration when supported, authorization requests, token exchange, refresh,
and authenticated HTTP requests. Eggy supplies the owner interaction and
durable credential storage required by a headless daemon.

OAuth state is stored separately per server:

```text
<data_dir>/mcp/<server-name>/oauth.json
```

The file contains only the SDK state required to resume authorization and use
or refresh credentials. Sensitive fields are encrypted with Eggy's existing
encryption key. Writes use the repository's established file-locking,
temporary-file, fsync, and atomic-rename conventions. Removing MCP credentials
does not alter `state.json` or the server definition in `config.yaml`.

The owner starts the standard flow with `/mcp login <server>`. Eggy sends the
authorization URL through the owner-only Telegram channel. The SDK flow uses
Eggy's public callback route when the authorization server supports a redirect
callback; any continuation code required by the SDK is accepted only through
the same owner-only command surface. State is expiring, single-use, and bound
to the named server.

Successful authorization persists credentials and requests a controlled Eggy
restart so bootstrap can register the newly available tools. `/mcp logout
<server>` removes only that server's stored OAuth credentials and requests the
same controlled restart. A server without usable credentials is
`login_required` and contributes no tools.

## Discovery and tool projection

After the MCP initialization handshake, the adapter reads every `tools/list`
page. It validates each tool, applies the configured filter, and creates a
proxy implementing `ports.Tool`.

Projected names use the conventional server namespace:

```text
<server>__<normalized-tool-name>
```

For example:

```text
railway + list-projects -> railway__list_projects
railway + get-logs     -> railway__get_logs
```

Normalization replaces MCP-name characters that the model provider cannot
accept with underscores. The adapter rejects empty results and normalized
collisions; it never silently lets one tool replace another. Collisions with
native Eggy tools are prevented by the server namespace and still checked by
the existing registry.

The remote description and input schema become the `ports.ToolDefinition`.
MCP annotations remain informational metadata; they do not override Eggy's
turn-level tool availability or existing approval boundaries.

`tools/list_changed` marks the affected server's catalog `reload_required`.
Eggy's outer loop and tool registry remain a fixed bootstrap snapshot, so a
tool cannot appear or disappear midway through model execution. The owner can
apply the changed catalog with `/mcp reload`; its Eggy implementation uses the
existing controlled process-restart hook and rebuilds the complete registry at
bootstrap. This mirrors a conventional host reload without adding a mutable
registry or changing the agent loop.

## Tool execution and results

When the model calls a projected tool, its proxy sends the original remote
tool name and JSON arguments through the server's SDK session. It applies the
configured request timeout and respects caller cancellation.

The adapter converts the MCP result into a bounded JSON object:

```json
{
  "content": [],
  "structured_content": {},
  "is_error": false
}
```

Text and structured content are preserved. MCP execution errors (`isError`)
remain ordinary tool results so the model can correct its request. Protocol,
transport, authentication, timeout, and cancellation failures become
sanitized Go errors, which the existing loop already converts into a
model-visible error result.

Binary image or audio content is not injected as base64 in v1. The adapter
returns bounded metadata describing the unsupported content. Embedded
resources and resource links are likewise represented as bounded metadata;
resource reading is a non-goal for the first version.

The adapter enforces `max_output_bytes` after encoding. Oversized results are
replaced with a structured truncation error rather than cut into invalid or
misleading JSON. Authorization headers, tokens, OAuth verifier/state values,
and raw SDK traces never appear in returned errors or diagnostics.

## Agent availability

MCP tools join only the existing outer conversational tool registry.

- Direct owner messages receive the configured MCP tools.
- Heartbeat turns receive the existing explicit allowlist and therefore no MCP
  tools.
- Scheduled agent turns receive the existing read-only allowlist and therefore
  no MCP tools.
- Deterministic scheduled messages do not enter the model loop.
- The separate implementation loop is constructed from implementation tools
  only and therefore receives no MCP tools.

No MCP-specific permission engine is added. Existing Calendar mutation and
repository shipping authorization remain unchanged because MCP neither
replaces nor bypasses those services.

## Runtime lifecycle and failure isolation

Each enabled server has an independent runtime and status:

- `disabled`: configured but not connected.
- `login_required`: OAuth credentials are absent or no longer usable.
- `connecting`: handshake or discovery is in progress.
- `ready`: connected with a filtered tool catalog.
- `unavailable`: connection or discovery failed.
- `cooldown`: repeated request or protocol failures temporarily pause calls.

An invalid bootstrap configuration fails startup. A valid but unavailable MCP
server does not: it contributes no tools, records a bounded diagnostic, and
allows Eggy and other servers to continue.

Authentication failures use the SDK refresh path. If refresh fails, the
runtime becomes `login_required` instead of retrying indefinitely. Transient
connection failures use bounded exponential backoff. Repeated call failures
activate a short per-server cooldown so a broken server cannot consume an
entire model turn. Successful calls reset the failure count.

The manager serializes connection/catalog mutations per server. Tool calls may
run concurrently only when `supports_parallel_tool_calls` is configured for
that server. Shutdown cancels pending work and waits only for a bounded grace
period before closing sessions.

## Operator commands

Eggy mirrors the conventional MCP-host command surface:

- `/mcp list` lists configured servers and their current status.
- `/mcp status [server]` shows resolved transport, auth state, timeouts,
  filters, tool count, list-change support, and bounded diagnostics without
  exposing credentials.
- `/mcp probe [server]` opens a live connection, initializes it, lists
  capabilities and tools, and then closes the probe session.
- `/mcp login <server>` starts or continues SDK OAuth authorization.
- `/mcp logout <server>` removes stored OAuth credentials and closes the
  runtime.
- `/mcp reload [server]` validates the named server, then uses Eggy's existing
  controlled restart hook to reconstruct runtime state and the fixed tool
  registry from current configuration and credentials. The optional server
  argument scopes validation and diagnostics; the process restart rebuilds all
  servers.

These are deterministic owner commands; they do not enter the model loop.
Configuration remains file-owned in v1. The commands do not add, edit, or
remove server definitions.

## Testing

Adapter tests use SDK-supported in-memory or fake transports rather than live
network servers wherever possible. They cover:

- initialization and capability negotiation;
- paginated tool discovery;
- include/exclude filtering;
- name normalization and collision rejection;
- proxy schema and call translation;
- text, structured, MCP-error, unsupported-content, and oversized results;
- connection and request timeouts;
- cancellation and shutdown;
- list-change invalidation and `reload_required` diagnostics;
- failure counters and cooldown recovery;
- OAuth login state, persistence, refresh, logout, and diagnostic redaction;
- independent failure of multiple configured servers; and
- concurrent calls under the race detector.

Bootstrap and agent tests prove that:

- only enabled, connected, filtered tools are registered;
- direct owner turns see MCP tools;
- heartbeat, scheduled, and implementation turns do not;
- native tool collisions fail safely;
- missing MCP credentials do not make Eggy unready; and
- fake-adapter mode requires no Railway account or network access.

Automated completion requires a focused adapter test followed by:

```text
make fmt vet test race build
```

`make smoke` runs when Docker is available. Live Railway verification is a
separate manual step: authenticate, run `/mcp probe railway`, list projects,
and retrieve bounded logs without invoking `list-variables`.

## Implementation sequence constraints

The implementation plan must preserve these boundaries:

1. Add behavior test-first, starting with adapter discovery and projection.
2. Add only the official Go MCP SDK and its required dependencies; do not add
   an agent, DI, web, plugin, or OAuth framework.
3. Keep all SDK and provider types inside `internal/adapters/tools/mcp`.
4. Put configuration, construction, tool registration, command wiring, HTTP
   callback wiring, and fake-adapter selection in `internal/bootstrap`.
5. Do not change `ports.Tool`, the agent-loop protocol, or existing provider
   interfaces to fit MCP.
6. Keep existing Calendar, repository, runner, approval, scheduling, and state
   behavior unchanged.
7. Verify automated behavior without requiring live Railway credentials, then
   report live verification separately.

## References

- MCP Go SDK: <https://github.com/modelcontextprotocol/go-sdk>
- Hermes MCP client: <https://github.com/NousResearch/hermes-agent/blob/main/tools/mcp_tool.py>
- OpenClaw MCP client registry: <https://github.com/openclaw/openclaw/blob/main/docs/cli/mcp.md>
- Pi coding-agent architecture: <https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md>
- Railway MCP server: <https://docs.railway.com/ai/mcp-server>
- Railway agent integration guidance: <https://docs.railway.com/agents>
