# Configurable Coding Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persisted Telegram selection between configured Codex CLI and Claude Code coding-agent adapters without weakening Eggy's runner or approval boundaries.

**Architecture:** Bootstrap builds provider-specific CLI adapters from a provider-neutral coding registry. A kernel `CodingAgentRuntime` persists the selected alias and delegates each run to one `ports.CodingAgent`, retaining the run-to-agent association for cancellation. Provider credentials are owned and injected by adapters rather than `CodingService`.

**Tech Stack:** Go 1.26 standard library, YAML v3, Codex CLI JSONL, Claude Code stream JSON, Docker, Railway.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral.
- Register adapters only through `internal/bootstrap`.
- Preserve runner root, environment, timeout, output, and process-group restrictions.
- Preserve independent commit, push, pull-request, and Calendar approvals and protected-branch denial.
- Preserve `/data/state.json` schema compatibility with an additive optional field and no schema-version change.
- Write each behavior test-first and run `make fmt vet test race build` before completion.
- Do not issue live Anthropic requests from the automated test suite.

---

### Task 1: Persisted provider-neutral coding-agent selection

**Files:**
- Modify: `internal/ports/ports.go`
- Create: `internal/kernel/services/coding_runtime.go`
- Create: `internal/kernel/services/coding_runtime_test.go`
- Modify: `internal/adapters/state/jsonfile/store_test.go`

**Interfaces:**
- Consumes: `ports.CodingAgent`, `ports.StateStore`, `ports.CodingRequest`, `ports.CodingProgress`, `ports.CodingResult`.
- Produces: `services.NewCodingAgentRuntime(store ports.StateStore, defaultAlias string, agents map[string]ports.CodingAgent) (*CodingAgentRuntime, error)`, `Selected(context.Context)`, `Select(context.Context, string)`, `Aliases()`, `Run(...)`, and `Interrupt(string)`.

- [ ] **Step 1: Write failing runtime tests**

Cover default selection, persisted selection, reset with an empty alias, unknown alias rejection, delegation to the selected fake agent, and interruption routed to the agent that started the run even after selection changes.

- [ ] **Step 2: Verify the tests fail for the missing runtime**

Run: `go test ./internal/kernel/services -run CodingAgentRuntime -count=1`

Expected: compilation fails because `NewCodingAgentRuntime` and coding runtime state do not exist.

- [ ] **Step 3: Add additive state and the minimal runtime**

Add:

```go
type CodingRuntimeState struct {
    SelectedAgent string `json:"selected_agent,omitempty"`
}
```

Embed it in `ports.State` as `Coding CodingRuntimeState ` followed by the JSON tag `json:"coding,omitempty"`. Implement selection with bounded optimistic-conflict retries, sorted alias copies, run-to-agent tracking under a mutex, and no provider-specific names.

- [ ] **Step 4: Verify runtime and persistence tests pass**

Run: `go test ./internal/kernel/services ./internal/adapters/state/jsonfile -run 'CodingAgentRuntime|MigratesSchemaOne|StoreCreates' -count=1`

Expected: PASS, including legacy state with an empty coding selection.

### Task 2: Configuration and owner command

**Files:**
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/config_test.go`
- Modify: `config.example.yaml`
- Modify: `internal/bootstrap/commands.go`
- Modify: `internal/bootstrap/commands_test.go`
- Modify: `internal/adapters/channels/telegram/commands.go`
- Modify: `internal/adapters/channels/telegram/commands_test.go`

**Interfaces:**
- Consumes: `services.CodingAgentRuntime` from Task 1.
- Produces: `CodingConfig{DefaultAgent string, Agents map[string]CodingAgentConfig}`, generic `Secrets.CodingAgentCredentials`, and `/coding_agent [alias|default]`.

- [ ] **Step 1: Write failing config and command tests**

Test version 1 and omitted version 2 normalization to Codex, strict validation for supported adapters and credential environment names, secret loading without secret persistence, default-agent credential requirements, and command list/select/reset/error output.

- [ ] **Step 2: Verify focused tests fail**

Run: `go test ./internal/bootstrap ./internal/adapters/channels/telegram -run 'Coding|CommandRegistry' -count=1`

Expected: FAIL because coding configuration and `/coding_agent` are absent.

- [ ] **Step 3: Implement configuration and command behavior**

Use this YAML shape:

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

Accept only `codex_cli` and `claude_cli` in bootstrap. Require a credential only when the configured default names a `credential_env`; optional non-default agents may be omitted from runtime registration when their credential is absent. Add the Telegram command suggestion and remove Codex-specific wording from `/runs` and `/stop`.

- [ ] **Step 4: Verify focused tests pass**

Run: `go test ./internal/bootstrap ./internal/adapters/channels/telegram -run 'Coding|CommandRegistry' -count=1`

Expected: PASS.

### Task 3: Adapter-owned environments and Claude Code CLI adapter

**Files:**
- Modify: `internal/adapters/coding/codexcli/codex.go`
- Modify: `internal/adapters/coding/codexcli/codex_test.go`
- Create: `internal/adapters/coding/claudecli/claude.go`
- Create: `internal/adapters/coding/claudecli/claude_test.go`
- Modify: `internal/kernel/services/coding.go`
- Modify: `internal/kernel/services/coding_test.go`

**Interfaces:**
- Consumes: `ports.Runner`, `ports.StreamingRunner`, and provider-neutral coding requests/results.
- Produces: `codexcli.New(executable string, runner ports.Runner, maxOutput int64, home string)` and `claudecli.New(executable string, runner ports.Runner, maxOutput int64, oauthToken, configDir string)`.

- [ ] **Step 1: Write failing adapter tests**

Test that Codex injects only `CODEX_HOME`; Claude injects only `CLAUDE_CODE_OAUTH_TOKEN` and `CLAUDE_CONFIG_DIR`; modifying Claude runs use `bypassPermissions`; read-only runs use `plan`; stream events normalize started/tool/completed progress; structured final JSON maps to `ports.CodingResult`; malformed results fail; cancellation reaches the runner.

- [ ] **Step 2: Verify adapter tests fail**

Run: `go test ./internal/adapters/coding/... -count=1`

Expected: compilation fails because the Claude adapter does not exist and the Codex constructor lacks adapter-owned home configuration.

- [ ] **Step 3: Implement minimal adapters**

Invoke Claude with:

```text
claude -p --output-format stream-json --verbose --permission-mode <plan|bypassPermissions> <instruction>
```

Parse `system/init`, assistant `tool_use`, retry/error, and final `result` events. The final `result` string must decode into non-empty `summary`; modification runs also require a non-empty `commit_message`. Never include credential values in returned errors.

Remove `codexHome` and provider environment construction from `CodingService`; it sends a plain `ports.CodingRequest` to the runtime.

- [ ] **Step 4: Verify adapter and coding-service tests pass**

Run: `go test ./internal/adapters/coding/... ./internal/kernel/services -run 'Codex|Claude|CodingService' -count=1`

Expected: PASS.

### Task 4: Bootstrap composition and capability reporting

**Files:**
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_test.go`
- Modify: `internal/kernel/agent/prompt.go`
- Modify: `internal/kernel/agent/prompt_test.go`

**Interfaces:**
- Consumes: configured adapters and `CodingAgentRuntime` from prior tasks.
- Produces: one selected runtime injected into `CodingService`, command service, readiness checks, and provider-neutral capability text.

- [ ] **Step 1: Write failing bootstrap tests**

Test Codex-only compatibility, Claude registration when its Railway secret and executable are present, rejection when Claude is the unavailable default, runtime switching through `App.ExecuteCommand`, and capability output that identifies the selected coding alias without claiming all runs use Codex.

- [ ] **Step 2: Verify bootstrap tests fail**

Run: `go test ./internal/bootstrap ./internal/kernel/agent -run 'CodingAgent|Capability' -count=1`

Expected: FAIL because bootstrap still hardcodes Codex.

- [ ] **Step 3: Wire adapters exclusively in bootstrap**

Add `ClaudeExecutable` to `AppOptions`, resolve only configured/credential-ready agents, construct `CodingAgentRuntime`, pass it to the coding and command services, allow only the selected adapters' credential environment names in the runner, and update readiness integration names from hard-coded `codex` to configured coding aliases.

- [ ] **Step 4: Verify bootstrap tests pass**

Run: `go test ./internal/bootstrap ./internal/kernel/agent -run 'CodingAgent|Capability' -count=1`

Expected: PASS.

### Task 5: Production packaging and operator documentation

**Files:**
- Modify: `Dockerfile`
- Modify: `README.md`
- Modify: `config.example.yaml`

**Interfaces:**
- Consumes: `CLAUDE_CODE_OAUTH_TOKEN` already configured in Railway.
- Produces: a production image containing pinned Claude Code `2.1.215` and documented runtime selection/authentication.

- [ ] **Step 1: Write a failing manifest assertion**

Extend an existing bootstrap or smoke manifest test to require the Claude package pin, `/data/claude`, and the documented Railway variable without embedding its value.

- [ ] **Step 2: Verify the assertion fails**

Run: `go test ./internal/bootstrap -run Docker -count=1`

Expected: FAIL because the Dockerfile lacks Claude Code.

- [ ] **Step 3: Update the image and README**

Install `@anthropic-ai/claude-code@2.1.215`, create `/data/claude`, set `CLAUDE_CONFIG_DIR=/data/claude`, document `claude setup-token`, `CLAUDE_CODE_OAUTH_TOKEN`, `/coding_agent`, default behavior, and one-year renewal. Keep secrets out of YAML.

- [ ] **Step 4: Run focused package checks**

Run: `go test ./internal/bootstrap ./internal/adapters/coding/... -count=1`

Expected: PASS.

### Task 6: Full verification and direct main push

**Files:**
- Verify all changed files.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified commits pushed to `origin/main`.

- [ ] **Step 1: Format and inspect the patch**

Run: `make fmt && git diff --check && git status --short`

Expected: formatting succeeds, no whitespace errors, and only intended files are modified.

- [ ] **Step 2: Run the required repository matrix**

Run: `make vet test race build`

Expected: every command exits 0.

- [ ] **Step 3: Review security invariants**

Confirm no credential appears in config, state fixtures, prompts, logs, progress, diffs, or errors; kernel imports remain provider-neutral; protected-branch and approval code is unchanged; and Claude read-only mode retains post-run repository verification.

- [ ] **Step 4: Commit the implementation**

Run:

```bash
git add Dockerfile README.md config.example.yaml internal
git commit -m "feat: add configurable Claude coding agent"
```

- [ ] **Step 5: Re-run verification on committed HEAD and push**

Run: `make vet test race build && git status --short && git push origin main`

Expected: checks exit 0, the worktree is clean, and `origin/main` advances to the implementation commit.
