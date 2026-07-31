---
title: MCP servers
description: Connect trusted MCP servers over Streamable HTTP or stdio and control which remote tools Eggy exposes.
eyebrow: Configure
---

MCP servers extend Eggy's tool catalog at runtime. Tools are namespaced as `<server>__<normalized_tool>`, preventing a remote server from replacing a native tool.

> **Trust model:** placing a server in `config.yaml` is the approval. MCP tools do not receive per-call Eggy approvals.

## Streamable HTTP

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
      failure_threshold: 3
      cooldown: 30s
      tool_filter:
        include: [list-projects, get-logs]
        exclude: []
```

HTTP authentication may be `none`, `oauth`, or `bearer-env`. Bearer authentication names its token variable with `bearer_token_env`. OAuth uses PKCE, the callback `/auth/mcp/{server}/callback`, and encrypted records in `auth.json`.

## Authorizing an OAuth server

Start the flow from either surface:

- **Telegram:** `/mcp login <name>` returns the provider's authorization URL.
- **Web:** visit `/auth/mcp/{server}` while signed in; it redirects to the provider.

The start route is owner-gated in both cases. The callback deliberately is not — it is the provider's redirect, authenticated by the `state` parameter it carries. Completing a login connects the server immediately, so its tools appear on the next turn without a restart. `/mcp logout <name>` discards the stored credentials.

## Editing servers without a file

Both the web settings panel and Telegram's `/mcp` command write through the same `internal/config` helpers, under the same lock and validation. HTTP servers can be added, edited, enabled, disabled, and removed from either. stdio servers are file-only: a subprocess command line is not a chat argument or a web form field.

A config write takes effect on restart, because adapters are constructed at startup. Both surfaces say so on every write.

## Stdio

```yaml
mcp:
  servers:
    filesystem:
      transport: stdio
      auth: none
      command: npx
      args: [-y, "@modelcontextprotocol/server-filesystem", /data/repos]
      env_allowlist: []
      enabled: true
      connect_timeout: 20s
      timeout: 60s
      max_output_bytes: 131072
      failure_threshold: 3
      cooldown: 30s
      tool_filter:
        include: [read_text_file, list_directory]
```

A stdio server runs as the same operating-system user as Eggy. Only `PATH`, `HOME`, and explicitly allowlisted variables enter the child, but the process shares Eggy's filesystem permissions. Treat it as trusted code.

## Filters and failures

Use exact include and exclude filters to reduce the exposed surface. Consecutive failures quarantine only the failing tool for the configured cooldown; other tools and servers remain available.

MCP tools are available to direct owner turns. Scheduled turns use a read-only allowlist and do not inherit arbitrary external authority.
