# Eggy MCP Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generic remote MCP client to Eggy's outer agent loop, with Railway's hosted MCP server as the first configured integration.

**Architecture:** A new `internal/adapters/tools/mcp` package uses `github.com/modelcontextprotocol/go-sdk` to connect, discover, filter, namespace, and proxy remote MCP tools as the existing `ports.Tool` interface. Bootstrap owns configuration, registration, commands, callback routing, and lifecycle; OAuth follows OpenClaw's durable-provider pattern at the SDK `auth.OAuthHandler` seam because Go SDK v1.6.1 does not expose save/restore for dynamic registration and refresh state.

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk` v1.6.1, its existing `golang.org/x/oauth2` dependency, standard library HTTP/crypto/JSON, YAML configuration, file-backed encrypted OAuth state.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral; do not change `ports.Tool` or the agent-loop protocol.
- Keep all MCP SDK, OAuth wire, and provider types inside `internal/adapters/tools/mcp`.
- Register MCP tools and handlers only through `internal/bootstrap`.
- Support Streamable HTTP only in v1; no stdio, legacy SSE, resources, prompts, roots, sampling, elicitation, or MCP Apps.
- Expose MCP tools only on direct owner turns; heartbeat, scheduled, and implementation turns remain unchanged.
- Do not add a generic MCP approval system or bypass existing Calendar/repository authorization.
- Preserve `/data/state.json`; store MCP OAuth data separately under `<data_dir>/mcp/` with locking and atomic writes.
- Never place bearer tokens, client secrets, refresh tokens, PKCE verifiers, or OAuth state in YAML, diagnostics, prompts, or returned tool errors.
- Develop test-first; finish with `make fmt vet test race build` and run `make smoke` when Docker is available.
- Preserve the user's existing uncommitted `TODO.md` change.

---

## File map

**New adapter files**

- `internal/adapters/tools/mcp/config.go` — adapter-facing server configuration and validation.
- `internal/adapters/tools/mcp/types.go` — status/diagnostic types and exported manager API.
- `internal/adapters/tools/mcp/session.go` — narrow session/connector interfaces plus official SDK connector.
- `internal/adapters/tools/mcp/manager.go` — per-server runtimes, discovery, filtering, status, cooldown, close, and stale-catalog tracking.
- `internal/adapters/tools/mcp/tool.go` — `ports.Tool` proxy and argument/call translation.
- `internal/adapters/tools/mcp/result.go` — bounded MCP content conversion.
- `internal/adapters/tools/mcp/names.go` — stable `<server>__<tool>` normalization and collision checks.
- `internal/adapters/tools/mcp/oauth_store.go` — encrypted, locked, atomic OAuth record storage.
- `internal/adapters/tools/mcp/oauth.go` — durable OAuth discovery, registration, PKCE, callback exchange, refresh, and SDK handler.
- `internal/adapters/tools/mcp/fake.go` — deterministic fake manager path for tests/smoke.
- Matching `_test.go` files beside each focused unit.

**Modified composition and operator files**

- `go.mod`, `go.sum` — pin official MCP Go SDK v1.6.1.
- `internal/bootstrap/config.go`, `config_init.go`, `config_test.go`, `config_init_test.go` — MCP YAML, defaults, validation, and env-secret loading.
- `internal/bootstrap/app.go`, `app_test.go`, `heartbeat_isolation_test.go` — construct/register/close MCP and prove turn filtering.
- `internal/bootstrap/server.go`, `server_test.go` — generic MCP OAuth callback route.
- `internal/bootstrap/commands.go`, `commands_test.go` — `/mcp` command family and autocomplete/help coverage.
- `cmd/eggy/main.go` and tests — route live MCP CLI commands through a lightweight MCP-only bootstrap path instead of fake adapters.
- `config.example.yaml`, `.env.example`, `README.md`, `docs/ARCHITECTURE.md` — Railway example and operating instructions.

---

### Task 1: Pin the SDK and add strict MCP configuration

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_init.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `internal/bootstrap/config_init_test.go`
- Modify: `config.example.yaml`

**Interfaces:**
- Produces: `MCPConfig`, `MCPServerConfig`, `MCPToolFilterConfig`, and `Secrets.MCPBearerTokens map[string]string` for bootstrap wiring.
- Produces: normalized defaults of `10s` connect timeout, `60s` call timeout, and `131072` maximum output bytes.

- [ ] **Step 1: Add failing strict-load and validation tests**

```go
func TestLoadConfigAcceptsRailwayMCP(t *testing.T) {
    text := validConfig() + `
mcp:
  servers:
    railway:
      url: https://mcp.railway.com
      transport: streamable-http
      auth: oauth
      enabled: true
      tool_filter:
        include: [list-projects, get-logs]
`
    cfg, _, err := loadText(t, text, testSecrets())
    if err != nil { t.Fatal(err) }
    server := cfg.MCP.Servers["railway"]
    if server.ConnectTimeout.Value() != 10*time.Second || server.Timeout.Value() != time.Minute || server.MaxOutputBytes != 128<<10 {
        t.Fatalf("server defaults = %#v", server)
    }
}

func TestMCPConfigValidation(t *testing.T) {
    tests := []struct{ name, rewrite, want string }{
        {"https", "https://mcp.railway.com", "http://remote.test", "must use HTTPS"},
        {"transport", "streamable-http", "stdio", "unsupported transport"},
        {"auth", "auth: oauth", "auth: token", "unsupported auth"},
    }
    // Load each rewritten config and assert the named validation error.
}
```

- [ ] **Step 2: Run the focused config tests and confirm failure**

Run: `go test ./internal/bootstrap -run 'TestLoadConfigAcceptsRailwayMCP|TestMCPConfigValidation'`

Expected: FAIL because `Config` has no `MCP` field.

- [ ] **Step 3: Add the configuration types, normalization, validation, and secrets**

```go
type MCPConfig struct {
    Servers map[string]MCPServerConfig `yaml:"servers,omitempty"`
}

type MCPServerConfig struct {
    URL                       string              `yaml:"url"`
    Transport                 string              `yaml:"transport"`
    Auth                      string              `yaml:"auth"`
    BearerTokenEnv            string              `yaml:"bearer_token_env,omitempty"`
    OAuthScopes               []string            `yaml:"oauth_scopes,omitempty"`
    Enabled                   bool                `yaml:"enabled"`
    ConnectTimeout            Duration            `yaml:"connect_timeout"`
    Timeout                   Duration            `yaml:"timeout"`
    MaxOutputBytes            int64               `yaml:"max_output_bytes"`
    SupportsParallelToolCalls bool                `yaml:"supports_parallel_tool_calls"`
    ToolFilter                MCPToolFilterConfig `yaml:"tool_filter"`
}

type MCPToolFilterConfig struct {
    Include []string `yaml:"include,omitempty"`
    Exclude []string `yaml:"exclude,omitempty"`
}
```

Add `MCP MCPConfig` to `Config` and `commonConfigDocument`, include it in normalize/marshal paths, default each server, validate names/HTTPS/auth/timeout/filter entries, require `EGGY_ENCRYPTION_KEY` when an enabled server uses OAuth, and resolve `bearer_token_env` into `Secrets.MCPBearerTokens[name]` without storing values in `Config`.

- [ ] **Step 4: Pin and download the official SDK**

Run: `go get github.com/modelcontextprotocol/go-sdk@v1.6.1`

Expected: `go.mod` contains the direct MCP SDK dependency and `go.sum` contains its verified transitive dependencies.

- [ ] **Step 5: Add the Railway example configuration**

Add the approved `mcp.servers.railway` block from the design spec, including every curated Railway tool except `list-variables`.

- [ ] **Step 6: Run focused tests and commit**

Run: `go test ./internal/bootstrap -run 'Config|FirstBoot'`

Expected: PASS.

```bash
git add go.mod go.sum internal/bootstrap/config.go internal/bootstrap/config_init.go internal/bootstrap/config_test.go internal/bootstrap/config_init_test.go config.example.yaml
git commit -m "feat: configure remote MCP servers"
```

---

### Task 2: Discover and project MCP tools through `ports.Tool`

**Files:**
- Create: `internal/adapters/tools/mcp/config.go`
- Create: `internal/adapters/tools/mcp/session.go`
- Create: `internal/adapters/tools/mcp/names.go`
- Create: `internal/adapters/tools/mcp/result.go`
- Create: `internal/adapters/tools/mcp/tool.go`
- Create: `internal/adapters/tools/mcp/tool_test.go`
- Create: `internal/adapters/tools/mcp/session_test.go`

**Interfaces:**
- Produces: internal `clientSession` with `ListTools`, `CallTool`, and `Close` matching the SDK session methods.
- Produces: `remoteTool` implementing `ports.Tool`.
- Produces: `normalizeToolName(server, remote string) (string, error)`.
- Produces: `convertResult(*sdk.CallToolResult, maxBytes int64) (json.RawMessage, error)`.

- [ ] **Step 1: Write failing name, schema, call, and result tests**

```go
func TestRemoteToolProjectsDefinitionAndCall(t *testing.T) {
    session := &fakeSession{callResult: &sdk.CallToolResult{
        Content: []sdk.Content{&sdk.TextContent{Text: "two projects"}},
        StructuredContent: map[string]any{"count": 2},
    }}
    tool, err := newRemoteTool("railway", &sdk.Tool{
        Name: "list-projects", Description: "List projects",
        InputSchema: map[string]any{"type": "object", "additionalProperties": false},
    }, session, time.Second, 4096, nil)
    if err != nil { t.Fatal(err) }
    if got := tool.Definition().Name; got != "railway__list_projects" { t.Fatalf("name=%q", got) }
    out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
    if err != nil { t.Fatal(err) }
    if session.calledName != "list-projects" || !bytes.Contains(out, []byte(`"count":2`)) { t.Fatalf("call=%q out=%s", session.calledName, out) }
}
```

Also test invalid JSON arguments, empty/over-128-byte normalized names, normalized collisions, MCP `IsError`, oversized results, and image/audio content returning metadata rather than base64.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/adapters/tools/mcp`

Expected: FAIL because the package and functions do not exist.

- [ ] **Step 3: Implement the narrow session seam and SDK connector**

```go
type clientSession interface {
    ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
    CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
    Close() error
}

type connector func(context.Context, ServerConfig, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error)

func connectSDK(ctx context.Context, cfg ServerConfig, handler auth.OAuthHandler, opts *sdk.ClientOptions) (clientSession, error) {
    client := sdk.NewClient(&sdk.Implementation{Name: "eggy", Version: "1"}, opts)
    transport := &sdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: cfg.HTTPClient, OAuthHandler: handler}
    return client.Connect(ctx, transport, nil)
}
```

Set `ClientOptions.Capabilities = &sdk.ClientCapabilities{}` so Eggy does not advertise roots, sampling, or elicitation.

- [ ] **Step 4: Implement tool projection and bounded result conversion**

Strictly decode arguments into `map[string]any`, preserve the remote name for `CallTool`, pass cancellation through a timeout context, marshal text and structured content, convert binary/embedded content to type/size/MIME metadata, and return a structured `result_too_large` error instead of partial JSON.

- [ ] **Step 5: Run focused tests and commit**

Run: `go test ./internal/adapters/tools/mcp`

Expected: PASS.

```bash
git add internal/adapters/tools/mcp go.mod go.sum
git commit -m "feat: project MCP tools into the agent registry"
```

---

### Task 3: Add multi-server lifecycle, filtering, status, and cooldown

**Files:**
- Create: `internal/adapters/tools/mcp/types.go`
- Create: `internal/adapters/tools/mcp/manager.go`
- Create: `internal/adapters/tools/mcp/manager_test.go`
- Create: `internal/adapters/tools/mcp/fake.go`

**Interfaces:**
- Produces: `NewManager(ctx context.Context, configs []ServerConfig, options Options) (*Manager, error)`.
- Produces: `Manager.Tools() []ports.Tool`, `Statuses() []ServerStatus`, `Probe(ctx, name)`, `MarkReloadRequired(name)`, and `Close() error`.
- Consumes: `clientSession`, `connector`, `remoteTool`, and `normalizeToolName` from Task 2.

- [ ] **Step 1: Write failing manager tests**

```go
func TestManagerFiltersAndIsolatesServers(t *testing.T) {
    connect := fakeConnector(map[string]*fakeSession{
        "ready": {tools: []*sdk.Tool{{Name: "read"}, {Name: "secret"}}},
        "broken": {listErr: errors.New("offline")},
    })
    manager, err := newManager(context.Background(), []ServerConfig{
        {Name: "ready", Enabled: true, Filter: ToolFilter{Include: []string{"read"}}},
        {Name: "broken", Enabled: true},
    }, Options{Connect: connect, Now: time.Now})
    if err != nil { t.Fatal(err) }
    if names := toolNames(manager.Tools()); !slices.Equal(names, []string{"ready__read"}) { t.Fatalf("tools=%v", names) }
    if got := statusByName(manager.Statuses(), "broken").State; got != StateUnavailable { t.Fatalf("state=%s", got) }
}
```

Add tests for pagination, include/exclude precedence, missing includes as warnings, duplicate projected names, list-change marking `reload_required`, three consecutive call failures causing a 30-second cooldown, success resetting failures, independent servers, parallel-call serialization, and bounded shutdown.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/adapters/tools/mcp -run 'TestManager'`

Expected: FAIL because `Manager` does not exist.

- [ ] **Step 3: Implement manager runtime state**

```go
type ServerState string
const (
    StateDisabled ServerState = "disabled"
    StateLoginRequired ServerState = "login_required"
    StateReady ServerState = "ready"
    StateUnavailable ServerState = "unavailable"
    StateCooldown ServerState = "cooldown"
)

type ServerStatus struct {
    Name string
    State ServerState
    Tools int
    ReloadRequired bool
    Warnings []string
    Diagnostic string
}
```

Keep one runtime per server, fetch all `NextCursor` pages, apply exact include then exclude filters, sort projected tools, and install `ToolListChangedHandler` to mark the status stale. Wrap proxy execution with per-server failure accounting; never include SDK request bodies, headers, or tokens in `Diagnostic`.

- [ ] **Step 4: Implement fake-adapter mode**

`NewFakeManager` should produce deterministic empty-object schemas for configured include names without network or OAuth, while preserving disabled and filtered status behavior for bootstrap/smoke tests.

- [ ] **Step 5: Run focused and race tests, then commit**

Run: `go test -race ./internal/adapters/tools/mcp`

Expected: PASS.

```bash
git add internal/adapters/tools/mcp
git commit -m "feat: manage MCP server lifecycles"
```

---

### Task 4: Persist MCP OAuth state using the OpenClaw provider pattern

**Files:**
- Create: `internal/adapters/tools/mcp/oauth_store.go`
- Create: `internal/adapters/tools/mcp/oauth_store_test.go`
- Create: `internal/adapters/tools/mcp/oauth.go`
- Create: `internal/adapters/tools/mcp/oauth_test.go`

**Interfaces:**
- Produces: `OAuthStore` with `Load`, `Update`, and `Delete` per server/URL key.
- Produces: `BeginLogin(ctx, server) (authorizationURL string, error)`, `CompleteLogin(ctx, server, code, state string) error`, `Logout(server) error`, and an SDK `auth.OAuthHandler` for runtime connections.
- Produces: `ErrLoginRequired` as a sanitized adapter error.

- [ ] **Step 1: Write failing encrypted-store tests**

```go
func TestOAuthStoreRoundTripIsEncryptedAndAtomic(t *testing.T) {
    store, err := OpenOAuthStore(t.TempDir(), base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
    if err != nil { t.Fatal(err) }
    record := OAuthRecord{ServerURL: "https://mcp.example", ClientID: "client", AccessToken: "access-secret", RefreshToken: "refresh-secret"}
    if err := store.Update("railway", record.ServerURL, func(*OAuthRecord) error { return nil }, record); err != nil { t.Fatal(err) }
    raw, _ := os.ReadFile(store.path("railway", record.ServerURL))
    if bytes.Contains(raw, []byte("refresh-secret")) { t.Fatal("credential written in plaintext") }
    got, err := store.Load("railway", record.ServerURL)
    if err != nil || got.RefreshToken != record.RefreshToken { t.Fatalf("got=%#v err=%v", got, err) }
}
```

Also test random nonces, tamper rejection, file mode `0600`, locked concurrent updates, server-name/URL hash separation, deletion, and no `/data/state.json` writes.

- [ ] **Step 2: Run store tests and confirm failure**

Run: `go test ./internal/adapters/tools/mcp -run 'TestOAuthStore'`

Expected: FAIL because `OAuthStore` does not exist.

- [ ] **Step 3: Implement the versioned encrypted record**

```go
type OAuthRecord struct {
    Version int `json:"version"`
    ServerURL string `json:"server_url"`
    Resource string `json:"resource"`
    AuthorizationEndpoint string `json:"authorization_endpoint"`
    TokenEndpoint string `json:"token_endpoint"`
    RegistrationEndpoint string `json:"registration_endpoint,omitempty"`
    ClientID string `json:"client_id"`
    ClientSecret string `json:"client_secret,omitempty"`
    Scopes []string `json:"scopes,omitempty"`
    AccessToken string `json:"access_token,omitempty"`
    RefreshToken string `json:"refresh_token,omitempty"`
    TokenType string `json:"token_type,omitempty"`
    Expiry time.Time `json:"expiry,omitempty"`
    State string `json:"state,omitempty"`
    CodeVerifier string `json:"code_verifier,omitempty"`
    LastAuthorizationURL string `json:"last_authorization_url,omitempty"`
}
```

Encrypt the complete JSON record with AES-256-GCM, lock using `internal/adapters/filelock`, and persist through temp-file, sync, chmod, close, and rename.

- [ ] **Step 4: Write failing OAuth flow and refresh tests**

Use an in-memory `RoundTripper` to serve protected-resource metadata, authorization-server metadata, dynamic registration, token exchange, and refresh. Assert PKCE S256, state matching, persisted client information, persisted rotated refresh tokens, and a second manager instance restoring and refreshing without a new browser flow.

- [ ] **Step 5: Implement standard OAuth discovery and the SDK handler**

Port the established OpenClaw persistence behavior using the Go SDK's exported `oauthex.GetProtectedResourceMetadata`, `auth.GetAuthServerMetadata`, and `oauthex.RegisterClient`, plus `oauth2.Config` for authorization, exchange, and refresh. The runtime handler must return a persisting `oauth2.TokenSource` from `TokenSource`; `Authorize` must return sanitized `ErrLoginRequired` rather than opening an interactive flow during an agent call.

- [ ] **Step 6: Run focused and race tests, then commit**

Run: `go test -race ./internal/adapters/tools/mcp -run 'OAuth'`

Expected: PASS.

```bash
git add internal/adapters/tools/mcp/oauth_store.go internal/adapters/tools/mcp/oauth_store_test.go internal/adapters/tools/mcp/oauth.go internal/adapters/tools/mcp/oauth_test.go
git commit -m "feat: persist MCP OAuth sessions"
```

---

### Task 5: Wire MCP into bootstrap, HTTP callbacks, and turn filtering

**Files:**
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/bootstrap/server.go`
- Modify: `internal/bootstrap/server_test.go`
- Modify: `internal/bootstrap/heartbeat_isolation_test.go`

**Interfaces:**
- Consumes: `mcpadapter.NewManager`, `NewFakeManager`, `Tools`, `Close`, and OAuth completion from Tasks 3-4.
- Produces: one optional `*mcpadapter.Manager` owned by `App` and exposed to `CommandService`.

- [ ] **Step 1: Write failing bootstrap and source-filtering tests**

```go
func TestNewAppRegistersMCPToolsOnlyForDirectOwnerTurns(t *testing.T) {
    cfg := appTestConfig(t.TempDir())
    cfg.MCP.Servers = map[string]MCPServerConfig{
        "railway": {Enabled: true, ToolFilter: MCPToolFilterConfig{Include: []string{"list-projects"}}},
    }
    app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
    if err != nil { t.Fatal(err) }
    direct := app.loop.ToolNames(agent.RunOptions{})
    scheduled := app.loop.ToolNames(readOnlyRunOptions())
    if !slices.Contains(direct, "railway__list_projects") || slices.Contains(scheduled, "railway__list_projects") {
        t.Fatalf("direct=%v scheduled=%v", direct, scheduled)
    }
    if slices.Contains(app.implementationLoop.ToolNames(agent.RunOptions{}), "railway__list_projects") { t.Fatal("MCP leaked into implementation loop") }
}
```

Add tests for an unavailable real manager not failing readiness, partial-bootstrap cleanup, `App.Run` closing sessions, and callback routing at `GET /auth/mcp/{server}/callback`.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/bootstrap -run 'MCP|SourceFiltering|HTTPHandler'`

Expected: FAIL because bootstrap does not construct MCP.

- [ ] **Step 3: Construct and register MCP in `NewApp`**

Translate bootstrap config/secrets into adapter `ServerConfig`, construct the fake or real manager, append `manager.Tools()` to the existing `services.ToolRegistry`, retain the manager on `App`, pass it to `CommandService`, and add MCP bearer/OAuth values to `activeSecrets`. Ensure every return after construction closes the manager.

- [ ] **Step 4: Add callback and shutdown wiring**

Extend the HTTP composition function with one optional MCP callback handler mounted at `GET /auth/mcp/{server}/callback`. On successful completion invoke `RequestRestart`. Add `defer a.mcp.Close()` at the start of `App.Run` when configured.

- [ ] **Step 5: Run focused and race tests, then commit**

Run: `go test -race ./internal/bootstrap -run 'MCP|Heartbeat|Schedule|HTTPHandler'`

Expected: PASS.

```bash
git add internal/bootstrap/app.go internal/bootstrap/app_test.go internal/bootstrap/server.go internal/bootstrap/server_test.go internal/bootstrap/heartbeat_isolation_test.go
git commit -m "feat: wire MCP tools into Eggy"
```

---

### Task 6: Add standard MCP management commands

**Files:**
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`
- Create: `internal/bootstrap/mcp_commands_test.go`
- Modify: `cmd/eggy/main.go`
- Modify: `cmd/eggy/main_test.go` if present, otherwise create `cmd/eggy/mcp_test.go`

**Interfaces:**
- Consumes: manager status/probe/login/logout APIs.
- Produces: `/mcp`, `/mcp status`, `/mcp probe`, `/mcp login`, `/mcp logout`, `/mcp reload` through the shared Telegram/CLI catalog.

- [ ] **Step 1: Write failing command tests**

```go
func TestMCPCommandsUseManager(t *testing.T) {
    commands := &CommandService{mcp: fakeMCPCommands{statuses: []mcpadapter.ServerStatus{{Name: "railway", State: mcpadapter.StateReady, Tools: 3}}}}
    for _, input := range []string{"/mcp", "/mcp status railway", "/mcp probe railway", "/mcp login railway", "/mcp logout railway", "/mcp reload railway"} {
        output, handled, err := commands.Execute(context.Background(), input)
        if err != nil || !handled || output == "" { t.Fatalf("%s output=%q handled=%v err=%v", input, output, handled, err) }
    }
}
```

Add usage-error tests, missing-server tests, status redaction assertions, Telegram autocomplete/help coverage, and a login result containing the authorization URL without any token/state/verifier.

- [ ] **Step 2: Run command tests and confirm failure**

Run: `go test ./internal/bootstrap ./cmd/eggy -run 'MCP'`

Expected: FAIL because the catalog has no MCP commands.

- [ ] **Step 3: Add the command interface and handlers**

Define a small bootstrap-local `MCPCommands` interface so command tests do not depend on live SDK sessions:

```go
type MCPCommands interface {
    Statuses() []mcpadapter.ServerStatus
    Status(string) (mcpadapter.ServerStatus, error)
    Probe(context.Context, string) (mcpadapter.ProbeResult, error)
    BeginLogin(context.Context, string) (string, error)
    Logout(string) error
}
```

Add `mcp` to `topLevelCommandOrder`, register all six catalog paths with canonical Telegram/CLI examples, render bounded tables/fields, and call `restart` after logout/reload.

- [ ] **Step 4: Give `eggy mcp` a lightweight live path**

Route CLI arguments beginning with `mcp` to a bootstrap helper that loads only MCP config/secrets and constructs the MCP manager; do not construct Telegram, model, repository, scheduler, or coding adapters. Ensure the helper closes the manager before returning.

- [ ] **Step 5: Run command/docs-consistency tests and commit**

Run: `go test ./internal/bootstrap ./cmd/eggy -run 'MCP|Catalog|ReadmeDocumentsCatalogCommands'`

Expected: command tests pass; the README consistency test initially points to the documentation work in Task 7, so update the README command list before committing if required.

```bash
git add internal/bootstrap/commands.go internal/bootstrap/commands_test.go internal/bootstrap/mcp_commands_test.go cmd/eggy/main.go cmd/eggy/mcp_test.go
git commit -m "feat: manage MCP servers from Eggy"
```

---

### Task 7: Document Railway MCP operation and verify end to end

**Files:**
- Modify: `.env.example`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `internal/bootstrap/docs_consistency_test.go` only if a deliberate command exception is required; prefer documenting every `/mcp` command.

**Interfaces:**
- Consumes: the final configuration and command names from Tasks 1 and 6.
- Produces: exact local/Railway setup, OAuth login, probe, filtering, and troubleshooting instructions.

- [ ] **Step 1: Write the operator documentation**

Document:

```text
1. Enable the railway server in config.yaml.
2. Set EGGY_ENCRYPTION_KEY; do not add a Railway token for OAuth mode.
3. Restart Eggy and run /mcp login railway.
4. Open the returned Railway URL and authorize the intended workspace/projects.
5. After the controlled restart, run /mcp probe railway and /mcp status railway.
6. Add future remote servers under mcp.servers using oauth, bearer-env, or none.
```

Explain that `list-variables` is excluded initially, MCP is direct-owner only, server failures are non-fatal, list changes require `/mcp reload`, and live verification is separate from automated tests.

- [ ] **Step 2: Update the architecture diagram and command reference**

Add the MCP manager alongside other adapters, show remote servers outside Eggy, and list `/mcp` commands in README so `TestReadmeDocumentsCatalogCommands` remains meaningful.

- [ ] **Step 3: Run focused documentation and adapter tests**

Run: `go test ./internal/adapters/tools/mcp ./internal/bootstrap ./cmd/eggy`

Expected: PASS.

- [ ] **Step 4: Run repository verification**

Run: `make fmt vet test race build`

Expected: all targets pass with no race reports.

- [ ] **Step 5: Run smoke when available**

Run: `make smoke`

Expected: PASS when Docker is available; otherwise record the exact Docker blocker without treating automated Go verification as failed.

- [ ] **Step 6: Perform optional live Railway verification**

Run `/mcp login railway`, `/mcp probe railway`, invoke `railway__list_projects`, and retrieve bounded logs. Do not call `list-variables`. Record live verification separately and do not claim it when credentials or network access are unavailable.

- [ ] **Step 7: Commit documentation and any final verification fixes**

```bash
git add .env.example README.md docs/ARCHITECTURE.md internal/bootstrap/docs_consistency_test.go
git commit -m "docs: explain Railway MCP setup"
```

---

## Final review checklist

- [ ] `git diff --check` is clean.
- [ ] `git status --short` contains no accidental staging of the user's `TODO.md` edit.
- [ ] `go list -m github.com/modelcontextprotocol/go-sdk` reports `v1.6.1`.
- [ ] No MCP SDK types appear under `internal/kernel` or `internal/ports`.
- [ ] No token, refresh token, client secret, state, verifier, or authorization header appears in test output or documentation examples.
- [ ] Direct owner requests see namespaced MCP tools; scheduled, heartbeat, and implementation loops do not.
- [ ] One unavailable server does not fail readiness or hide another ready server.
- [ ] OAuth credentials survive a new manager/process instance and refresh atomically.
- [ ] `/data/state.json` schema and migration tests are unchanged.
- [ ] `make fmt vet test race build` passes.
- [ ] `make smoke` result is reported accurately.
