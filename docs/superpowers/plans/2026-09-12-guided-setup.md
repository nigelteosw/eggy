# Guided Setup and Runtime Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task inline, without subagents. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a fresh operator configure and start Eggy in the web panel, then manage accounts and securely pair Telegram users at runtime without manually encoding identities in environment variables.

**Architecture:** Add a token-protected setup-only HTTP surface to the existing supervisor, with YAML writes owned by `internal/config` and deployment secrets supplied by the operator's environment. Make authorization resolve the validated config document at call time, then add a short-lived SQLite Telegram pairing record consumed by the existing webhook; no new agent loop, tool, role system, or persistence format is introduced.

**Tech Stack:** Go 1.26 standard library, `gopkg.in/yaml.v3`, SQLite, React, TypeScript, Bun's built-in test runner, existing Eggy web components.

**Spec:** `docs/superpowers/specs/2026-09-12-guided-setup-design.md`

**Status:** Updated 2026-09-13 for the agreed credential boundary. Planning only; execution and commits are separate work. Commit steps below are checkpoints for an explicitly authorized implementation workflow.

**Implementation status (2026-09-13):** Tasks 1-8 are implemented and their
focused suites pass, including `plugins/store/sqlite`'s pairing hardening
tests (replay, release, concurrent-claim) and the new
`internal/bootstrap/telegram_pairing_test.go` covering crash/restart,
config-write-failure release, and finalization-failure-after-linking at the
consumer boundary. `internal/bootstrap/setup_integration_test.go` adds the
Task 10 fresh-install path: no config on disk through `CompleteSetup`, App
boot, and a Telegram sender pairing through the real webhook with no secret
value reaching config.yaml or the startup log. `make fmt vet test race build`
and `make smoke` (Docker) all pass; `cd website && bun run test && bun run
build` and `cd docs && bun run build` pass. Task 9 is partially done: the
Telegram enable/webhook-registration/Link-Unlim flow is documented in
`use/telegram.md` and `configure/accounts.md`'s stale manual
`telegram_user_id` guidance is corrected, but the full operator-journey
rewrite (quickstart, Railway, security, troubleshooting leading with the
setup URL) and the richer `docs_consistency_test.go` assertions this task
describes are not done. Browser/mobile manual verification of the setup and
accounts UI was not performed in this pass; only Bun contract tests and a
production build were run.

**Delivery order:** Tasks 1-3 deliver guided setup; Task 4 delivers runtime accounts; Tasks 5-8 deliver Telegram pairing; Tasks 9-10 verify and document the complete journey. Task 3 uses the Task 5 Telegram enablement contract: ship setup initially with Telegram omitted, then expose its enable control with Task 5. Existing settings own optional extensions.

## Global Constraints

- Secrets remain in process environment variables or Eggy's home `.env`; secret values never enter YAML, JSON responses, logs, traces, conversation history, or model prompts.
- Every YAML write goes through `internal/config` under the existing file lock, validation, atomic-write, permission, and secret-redaction rules. Setup accepts no credential values, generates no deployment encryption key, and never writes `.env` or platform secrets.
- Eggy's bot credentials (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`) stay in the deployment environment. Account IDs, Google emails, and Telegram sender mappings are runtime configuration.
- Optional extension credentials, including GitHub tokens, provider keys, and MCP credentials, are required only for configured capabilities. The selected model key is needed for chat; GitHub, MCP, Telegram, and Workspace are optional.
- Google Workspace remains exclusively Eggy's shared identity, verified against `google.expected_email`, with its grant sealed in SQLite. User sign-in never connects a personal Workspace account.
- Workspace authorization is confined to Eggy's dedicated service identity for security: the ordinary Workspace user specified by `AGENTS.md`, not a Google Cloud IAM service account. No service-account keys, domain-wide delegation, user impersonation, or personal-user grants are introduced. Preserve identity verification and generation-bound approvals across reconnect/disconnect.
- Setup mode constructs no model, tool registry, Telegram adapter, SQLite store, schedule loop, or agent loop.
- Existing config files and the headless `EGGY_ACCOUNTS`, `EGGY_OWNER_ID`, and `EGGY_TELEGRAM_OWNER_ID` first-boot path remain compatible.
- Runtime account lookup fails closed when `config.yaml` cannot be read or validated.
- An unmapped Telegram sender may only attempt `/start <pairing-code>` in a private chat; no generic selection, callback, or message may authorize pairing.
- No framework, role system, invitation subsystem, database, durable format, core tool, or background goroutine is added.
- Run focused tests first, then `make fmt vet test race build`; run `make smoke` only when Docker is available and report an unavailable daemon as blocked.
- Keep SQLite as the sole machine-managed store for this delivery. The separate PostgreSQL draft is outside this plan; preserve it for reference.

## File map

| Unit | Files | Responsibility |
| --- | --- | --- |
| Setup document generation | `internal/config/setup.go`, `setup_test.go`, `config_init.go` | Validate non-secret configuration against the deployment environment and atomically create `config.yaml`; preserve headless initialization. |
| Setup HTTP surface | `internal/web/setup.go`, `setup_test.go`, `web.go`, `cmd/eggyd/main.go` | Issue a setup session from the logged token, expose only setup routes, and hand completion back to the supervisor. |
| Setup UI | `website/src/SetupPage.tsx`, `SetupPage.test.tsx`, `api.ts`, `App.tsx` | Guided first-account and capability configuration with credential variable names and presence checks, never secret inputs. |
| Live account directory | `internal/bootstrap/account_directory.go`, `account_directory_test.go`, `app_wiring.go`, `app.go`, `internal/web/accounts.go` | Reload validated account identity for authorization and remove unnecessary account restarts. |
| Telegram configuration | `internal/config/config.go`, `accounts.go`, `config_validate.go`, `config_mutate.go` and tests | Represent explicit Telegram enablement separately from paired accounts and mutate pairings centrally. |
| Pairing persistence | `plugins/store/sqlite/telegram_pairing.go`, `telegram_pairing_test.go`, `machine.go` | Store hashed, account-bound, expiring, single-use pairing records in SQLite. |
| Pairing flow | `plugins/channels/telegram/handler.go`, tests, `internal/bootstrap/telegram.go`, `internal/web/telegram_pairing.go`, tests | Create deep links, consume `/start` pairings, update config, and use live resolvers. |
| Operator documentation | quickstart, Railway, accounts, Telegram, security, troubleshooting | Make guided setup primary and retain a clearly labeled headless path. |

---

### Task 1: Atomically create configuration using provisioned secrets

**Files:**
- Create: `internal/config/setup.go`
- Create: `internal/config/setup_test.go`
- Modify: `internal/config/config_init.go`
- Modify: `internal/config/config_init_test.go`

**Interfaces:**
- Produces: `type SetupInput struct { AccountID, GoogleEmail, PublicBaseURL, LoginClientID, LoginClientSecretEnv, ProviderName, ProviderBaseURL, ProviderAPIKeyEnv, ModelAlias, ModelID string; TelegramEnabled bool }`
- Produces: `func CompleteSetup(homePath, configPath string, input SetupInput, getenv func(string) string) error`
- Preserves: `LoadOrCreateConfig(path string, getenv func(string) string) (Config, Secrets, error)` and the existing headless first-boot behavior.

- [ ] **Step 1: Write failing atomicity and secret-boundary tests**

```go
func TestCompleteSetupWritesOnlyConfig(t *testing.T) {
    configPath := filepath.Join(t.TempDir(), "config.yaml")
    envPath := filepath.Join(filepath.Dir(configPath), ".env")
    input := validSetupInput()
    getenv := mapEnv(accountSecrets()) // provisioned deployment credentials
    if err := CompleteSetup(filepath.Dir(configPath), configPath, input, getenv); err != nil { t.Fatal(err) }
    cfgBody, _ := os.ReadFile(configPath)
    for _, name := range []string{input.ProviderAPIKeyEnv, input.LoginClientSecretEnv, "EGGY_ENCRYPTION_KEY"} {
        if value := getenv(name); value != "" && bytes.Contains(cfgBody, []byte(value)) { t.Fatal("secret written to YAML") }
    }
    if _, err := os.Stat(envPath); !errors.Is(err, os.ErrNotExist) { t.Fatal("setup created .env") }
    cfg, _, err := LoadConfig(configPath, getenv)
    if err != nil { t.Fatal(err) }
    if cfg.Accounts[0].ID != "you" { t.Fatal("wrong account") }
}

func TestCompleteSetupLeavesNoFilesWhenCandidateIsInvalid(t *testing.T) {
    root := t.TempDir()
    input := validSetupInput()
    input.GoogleEmail = "not-an-email"
    err := CompleteSetup(root, filepath.Join(root, "config.yaml"), input, mapEnv(accountSecrets()))
    if err == nil { t.Fatal("expected validation error") }
    for _, name := range []string{"config.yaml", ".env"} {
        if _, statErr := os.Stat(filepath.Join(root, name)); !errors.Is(statErr, os.ErrNotExist) { t.Fatalf("%s exists", name) }
    }
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/config -run 'CompleteSetup' -count=1`

Expected: FAIL because `SetupInput` and `CompleteSetup` do not exist.

- [ ] **Step 3: Implement the candidate builder and atomic config write**

Build the same provider/model/account structures currently emitted by `firstBootConfig`, recording credential variable names rather than values. Reuse full config and secret validation against `getenv`; require the operator-provisioned encryption key and sign-in secret, and only the credentials of configured capabilities. Atomically write `config.yaml` under the existing lock using a same-directory temporary file, `Chmod(0600)`, `Sync`, and rename. Refuse to overwrite a config created after setup began. Add tests that an existing `.env` remains byte-identical, missing required variables prevent completion, and absent GitHub/MCP/Workspace/Telegram credentials do not prevent web-only setup. Never generate or persist secret values.

- [ ] **Step 4: Preserve and test the explicit headless path**

Add a table case proving `LoadOrCreateConfig` still generates from `EGGY_ACCOUNTS`, and separate absence from invalid provisioning: when no config and none of the legacy/headless identity variables are present, return sentinel `ErrSetupRequired`; when any headless variable is present but incomplete, retain the precise validation error.

```go
var ErrSetupRequired = errors.New("setup required")
```

- [ ] **Step 5: Run config tests**

Run: `go test ./internal/config -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the config unit**

```bash
git add internal/config/setup.go internal/config/setup_test.go internal/config/config_init.go internal/config/config_init_test.go
git commit -m "feat(config): add atomic guided setup"
```

### Task 2: Serve a one-time, token-protected setup mode

**Files:**
- Create: `internal/web/setup.go`
- Create: `internal/web/setup_test.go`
- Modify: `internal/web/web.go`
- Modify: `cmd/eggyd/main.go`
- Create: `cmd/eggyd/main_test.go`

**Interfaces:**
- Consumes: `config.ErrSetupRequired`, `config.SetupInput`, `config.CompleteSetup`.
- Produces: `type SetupMode struct { ConfigPath, PublicBaseURL string; TokenHash [32]byte; Expires time.Time; Now func() time.Time; Complete func(config.SetupInput) error; Completed func() }`
- Produces: `func NewSetupModeHandler(SetupMode) http.Handler`.

- [ ] **Step 1: Write failing route-security tests**

Test that `GET /api/mode` returns `{"mode":"setup"}`, normal `/api/*` routes return 503, the raw token is absent from responses/loggable URLs, a wrong or expired token returns the same 401, a successful `POST /api/setup/session` sets `HttpOnly; SameSite=Strict; Path=/`, and completion without that cookie is refused.

```go
func TestSetupTokenIsExchangedOnceAndNeverReturned(t *testing.T) {
    token := "known-token"
    mode := testSetupMode(t, sha256.Sum256([]byte(token)))
    handler := NewSetupModeHandler(mode)
    first := postJSON(handler, "/api/setup/session", `{"token":"known-token"}`, "")
    if first.Code != http.StatusNoContent { t.Fatalf("status=%d body=%s", first.Code, first.Body.String()) }
    if strings.Contains(first.Body.String(), token) { t.Fatal("token echoed") }
    replay := postJSON(handler, "/api/setup/session", `{"token":"known-token"}`, "")
    if replay.Code != http.StatusUnauthorized { t.Fatalf("replay status=%d", replay.Code) }
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/web ./cmd/eggyd -run 'Setup' -count=1`

Expected: FAIL because setup mode is not implemented.

- [ ] **Step 3: Implement in-memory setup authorization**

Generate the token using `crypto/rand`, log one URL as `https://host/#setup=<base64url-token>`, compare SHA-256 hashes with `subtle.ConstantTimeCompare`, consume it on exchange, and keep the setup session only in an HttpOnly cookie signed with a second random in-memory key. Apply the existing login throttle and CSRF same-origin check to state-changing routes. Never accept a setup token from a query parameter or header that an access logger commonly records.

- [ ] **Step 4: Add supervisor routing**

In `main`, branch on `errors.Is(err, config.ErrSetupRequired)` before safe mode. Serve setup on `safeModeListen(getenv)`, close it gracefully after `CompleteSetup`, and continue the existing supervisor loop so normal startup performs the authoritative reload. Other startup failures must still enter `NewSafeModeHandler`.

- [ ] **Step 5: Run route and supervisor tests**

Run: `go test ./internal/web ./cmd/eggyd -run 'Setup|SafeMode' -count=1`

Expected: PASS, including existing safe-mode cases.

- [ ] **Step 6: Commit the setup surface**

```bash
git add internal/web/setup.go internal/web/setup_test.go internal/web/web.go cmd/eggyd/main.go cmd/eggyd/main_test.go
git commit -m "feat(web): serve protected first-run setup"
```

### Task 3: Build the guided setup screen

**Files:**
- Create: `website/src/SetupPage.tsx`
- Create: `website/tests/setup.test.ts`
- Modify: `website/src/api.ts`
- Modify: `website/src/App.tsx`
- Modify: `website/tests/navigation.test.ts`

**Interfaces:**
- Consumes: `GET /api/mode`, `POST /api/setup/session`, `POST /api/setup/complete`.
- Produces: `completeSetup(input: SetupInput): Promise<void>`, `exchangeSetupToken(token: string): Promise<void>`, `SetupPage`, and `consumeSetupFragment(location: Pick<Location, "hash" | "pathname" | "search">, history: Pick<History, "replaceState">, exchange: (token: string) => Promise<void>): Promise<void>` in `api.ts`.
- Mirror the existing `website/tests/accounts.test.ts` Bun test conventions. Use pure function/HTTP contract tests and browser verification for actual interaction; do not introduce Vitest or a DOM test framework.

- [ ] **Step 1: Write failing UI contract tests**

Cover fragment removal with `history.replaceState`, setup-session exchange, required account/public URL/Google/model fields, credential variable names, absent secret-value inputs, optional Telegram enablement, inline missing-variable feedback, and a completion state that waits for normal mode. Reject unknown credential-value fields in API submissions; readiness reports only required variable names and presence after setup authentication, never arbitrary environment contents.

```tsx
import { expect, test } from "bun:test";

// Extract the small token-consumption helper into api.ts and inject location,
// history and exchange collaborators; no DOM testing framework is required.
test("consumes the fragment before exchanging the setup credential", async () => {
  const events: string[] = [];
  await consumeSetupFragment(
    { hash: "#setup=secret-token", pathname: "/", search: "" },
    { replaceState: () => events.push("removed") },
    async token => { expect(token).toBe("secret-token"); events.push("exchanged"); },
  );
  expect(events).toEqual(["removed", "exchanged"]);
});
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `cd website && bun run test ./tests/setup.test.ts ./tests/navigation.test.ts`

Expected: FAIL because setup mode and `SetupPage` are unknown.

- [ ] **Step 3: Implement a restrained four-section form**

Use existing `Card`, `Input`, `Label`, `Button`, and alert styles. Present Account, Sign-in, Model, and (after Task 5) optional Telegram sections on one readable page; prefill public URL and existing provider defaults. Collect only ordinary settings and credential variable names. Display missing required variables with instructions to provision them in the deployment environment and restart to reload if necessary. Reuse provider configuration conventions; add no credential manager. Keep mobile layout stacked.

- [ ] **Step 4: Route setup mode before login mode**

Extend the mode response type to `"normal" | "safe" | "setup"`. In `App.tsx`, render `SetupPage` before any session request when mode is setup. Poll `/api/mode` after completion and replace the page only after normal mode is live.

- [ ] **Step 5: Run frontend tests and build**

Run: `cd website && bun run test && bun run build`

Expected: PASS.

- [ ] **Step 6: Commit the setup UI**

```bash
git add website/src/SetupPage.tsx website/tests/setup.test.ts website/src/api.ts website/src/App.tsx website/tests/navigation.test.ts
git commit -m "feat(ui): add guided first-run setup"
```

### Task 4: Resolve account authorization from live validated config

**Files:**
- Create: `internal/bootstrap/account_directory.go`
- Create: `internal/bootstrap/account_directory_test.go`
- Modify: `internal/bootstrap/app_wiring.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `internal/bootstrap/app_events.go`
- Modify: `internal/bootstrap/login.go`
- Modify: `internal/bootstrap/accounts_test.go`
- Modify: `internal/bootstrap/heartbeat_test.go`
- Modify: `website/src/AccountsCard.tsx`
- Modify: `internal/web/accounts.go`
- Modify: `internal/web/accounts_test.go`

**Interfaces:**
- Produces: `func newAccountDirectory(configPath string, getenv func(string) string, initial config.Config) web.AccountDirectory`.
- Preserves: `web.AccountDirectory`; callers do not receive config types.

- [ ] **Step 1: Write failing live-resolution tests**

Construct the directory, mutate the file through `config.AddAccount`, and assert `AccountForEmail` sees the addition without rebuilding. Remove the account and assert `Account` immediately fails closed. Corrupt the file and assert no new identity is authorized while an already authenticated request is denied on its next guard check.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/bootstrap ./internal/web -run 'AccountDirectory|Account.*Restart|RemovedAccount' -count=1`

Expected: FAIL because `accountDirectory` holds the boot snapshot and account response copy requires restart.

- [ ] **Step 3: Implement a config-backed directory**

Move `accountDirectory` from `app_wiring.go` into the focused file. On each method, call `config.LoadDocument(configPath)`, normalize/validate the document, and explicitly call `cfg.Validate()` (`LoadDocument` applies defaults but does not validate). Return no account on either error. Keep the boot config only as the legacy/test fallback when `configPath == ""`; do not cache successful reads because authorization must observe revocation immediately.

Wire this same directory into the dispatcher account predicate in `app.go`, schedule-owner validation and heartbeat enumeration in `app_events.go`, web sessions/login, and Telegram ingress/egress. Remove boot-snapshot authorization reads from those paths. Preserve startup-only configuration for provider construction. Test that a newly added account can actually send a chat and receive scheduled output, and a removed account cannot enqueue or receive new work.

Account creation must also initialize its runtime repository state using the same initializer used at boot, and create Markdown lazily through existing context-store behavior. Extract that focused initializer from the boot loop and invoke it through an account-change callback wired by bootstrap, before acknowledging addition. A failed initializer returns an explicit partial-failure response and is retryable without creating duplicate accounts. Test the first turn's repository list and default approval mode, not only successful login.

- [ ] **Step 4: Remove false restart requirements**

Change add/edit/remove responses and Accounts card notices to state that access changed now. Preserve restart messaging only for Google login-client changes, Telegram adapter enablement, and other collaborators that bootstrap constructs once.

- [ ] **Step 5: Run account and session tests**

Run: `go test ./internal/config ./internal/web ./internal/bootstrap -run 'Account|Session|Identity' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit live account resolution**

```bash
git add internal/bootstrap/account_directory.go internal/bootstrap/account_directory_test.go internal/bootstrap/app_wiring.go internal/bootstrap/app.go internal/web/accounts.go internal/web/accounts_test.go website/src/AccountsCard.tsx
git commit -m "feat(auth): apply account changes at runtime"
```

### Task 5: Separate Telegram enablement from paired identities

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/accounts.go`
- Modify: `internal/config/config_validate.go`
- Modify: `internal/config/config_mutate.go`
- Modify: `internal/config/accounts_test.go`
- Modify: `internal/config/config_mutate_test.go`
- Modify: `config.example.yaml`

**Interfaces:**
- Produces: `TelegramConfig.Enabled *bool`.
- Produces: `func LinkTelegramAccount(path, accountID string, telegramUserID int64) error`.
- Produces: `func UnlinkTelegramAccount(path, accountID string) error`.
- Changes: `Config.TelegramEnabled()` respects explicit true/false and preserves inference from existing sender mappings when the field is absent.

- [ ] **Step 1: Write failing classification and mutation tests**

Test enabled Telegram with zero paired accounts, disabled Telegram with bot secrets costing no adapter, legacy owner compatibility, duplicate sender refusal, unknown account refusal, link success, and unlink success. Assert mutation leaves the file byte-identical on every refusal.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/config -run 'TelegramEnabled|LinkTelegram|UnlinkTelegram' -count=1`

Expected: FAIL because enablement is currently inferred from account IDs.

- [ ] **Step 3: Implement the minimal config shape and mutations**

Require `TELEGRAM_BOT_TOKEN` and `TELEGRAM_WEBHOOK_SECRET` only when Telegram is explicitly enabled or legacy `owner_id` enables it. Reject `enabled: true` outside account mode unless a legacy owner exists. Use the existing `mutate` lock/validation path for link and unlink, and derive sender uniqueness from `Config.AccountForTelegram`.

- [ ] **Step 4: Run all config tests**

Run: `go test ./internal/config -count=1`

Expected: PASS, including strict YAML and example-config tests.

- [ ] **Step 5: Commit Telegram configuration**

```bash
git add internal/config/config.go internal/config/accounts.go internal/config/config_validate.go internal/config/config_mutate.go internal/config/accounts_test.go internal/config/config_mutate_test.go config.example.yaml
git commit -m "feat(config): enable Telegram before account pairing"
```

### Task 6: Persist single-use Telegram pairing codes

**Files:**
- Create: `plugins/store/sqlite/telegram_pairing.go`
- Create: `plugins/store/sqlite/telegram_pairing_test.go`
- Modify: `plugins/store/sqlite/machine.go`
- Modify: `plugins/store/sqlite/store.go`
- Modify: `plugins/store/sqlite/machine_test.go`

**Interfaces:**
- Produces: `type TelegramPairingStore interface { CreateTelegramPairing(context.Context, string, [32]byte, time.Time) error; ClaimTelegramPairing(context.Context, [32]byte, time.Time) (string, [16]byte, bool, error); FinishTelegramPairing(context.Context, [16]byte, bool) error; DeleteTelegramPairings(context.Context, string) error }` in `internal/web/telegram_pairing.go` at its consumer boundary. `FinishTelegramPairing(..., true)` deletes the claimed record; `false` releases it after a config-write failure.
- SQLite methods use the same signatures directly; no new provider-neutral port is added.

- [ ] **Step 1: Write failing store tests**

Test storing only a SHA-256 hash, successful claim/finalize, released claims becoming claimable again, replay refusal after finalization, equal response for unknown expired/missing hashes, replacement of an account's prior pending code, account deletion cleanup, and concurrent claims yielding exactly one winner.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./plugins/store/sqlite -run 'TelegramPairing' -count=1`

Expected: FAIL because the table and methods do not exist.

- [ ] **Step 3: Add the schema and transactional consume**

Add `telegram_pairings(account_id TEXT PRIMARY KEY, code_hash BLOB UNIQUE NOT NULL, expires_at INTEGER NOT NULL, claim_id BLOB UNIQUE)` without an account foreign key: configured accounts live in YAML, and neither machine state nor identity enrollment is an account registry. Use UTC Unix seconds for expiry comparisons (no lexicographic RFC3339 comparisons). Claim with one transaction: delete expired rows, assign a random claim ID only when `claim_id IS NULL`, and return the account ID. Finalization either deletes the matching claimed row or clears its claim ID. Raise `sqlite.MachineStateVersion` from 7 to 8 (or the next version if the tree advances). Register the schema in `plugins/store/sqlite/store.go` alongside existing schema initialization; verify a version-7 home upgrades without losing identity/session/private data and a newer version is refused.

- [ ] **Step 4: Run SQLite tests**

Run: `go test ./plugins/store/sqlite -count=1`

Expected: PASS, including migration and downgrade-protection tests.

- [ ] **Step 5: Commit pairing persistence**

```bash
git add plugins/store/sqlite/telegram_pairing.go plugins/store/sqlite/telegram_pairing_test.go plugins/store/sqlite/machine.go plugins/store/sqlite/machine_test.go plugins/store/sqlite/store.go
git commit -m "feat(sqlite): store expiring Telegram pairings"
```

### Task 7: Pair and unpair Telegram from the authenticated panel

**Files:**
- Create: `internal/web/telegram_pairing.go`
- Create: `internal/web/telegram_pairing_test.go`
- Modify: `internal/web/web.go`
- Modify: `internal/bootstrap/app.go`
- Modify: `website/src/AccountsCard.tsx`
- Modify: `website/tests/accounts.test.ts`
- Modify: `website/src/api.ts`

**Interfaces:**
- Consumes: SQLite pairing methods and `config.UnlinkTelegramAccount`.
- Produces: `POST /api/accounts/{id}/telegram/pairing` returning `{url, expires_at}` but never the raw code separately. This URL is itself a credential: no analytics, durable logs, traces, or browser storage.
- Produces: `DELETE /api/accounts/{id}/telegram`.

- [ ] **Step 1: Write failing authorization and response tests**

Verify only the signed-in account can create or cancel its pairing, 32 random bytes are generated, only the hash reaches the store, the URL is `https://t.me/<bot>?start=<base64url-code>`, errors never include a code, and unlinking another account is refused. Test the UI renders Link, pending expiry, and Unlink states without a numeric-ID input.

- [ ] **Step 2: Run focused backend and frontend tests**

Run: `go test ./internal/web -run 'TelegramPairing|UnlinkTelegram' -count=1`

Run: `cd website && bun run test ./tests/accounts.test.ts`

Expected: FAIL because the routes and controls do not exist.

- [ ] **Step 3: Implement pairing routes and UI**

Read the bot username once during Telegram bootstrap with the existing client or configured bot metadata and provide it to `WebUIConfig`; do not call Telegram on every button press. Generate codes with `crypto/rand`, store `sha256.Sum256(code)` with `now+10m`, and return only the deep link. Replace manual numeric input in ordinary account forms with Link/Unlink; retain numeric display and the legacy conversion input where migration needs the known sender.

- [ ] **Step 4: Run pairing route and UI tests**

Run: `go test ./internal/web ./internal/bootstrap -run 'Telegram|Account' -count=1`

Run: `cd website && bun run test ./tests/accounts.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit the authenticated pairing surface**

```bash
git add internal/web/telegram_pairing.go internal/web/telegram_pairing_test.go internal/web/web.go internal/bootstrap/app.go website/src/AccountsCard.tsx website/tests/accounts.test.ts website/src/api.ts
git commit -m "feat(web): start Telegram pairing from accounts"
```

### Task 8: Consume pairings safely in the existing Telegram webhook

**Files:**
- Modify: `plugins/channels/telegram/handler.go`
- Modify: `plugins/channels/telegram/telegram_test.go`
- Modify: `internal/bootstrap/telegram.go`
- Modify: `internal/bootstrap/app.go`
- Create: `internal/bootstrap/telegram_pairing_test.go`

**Interfaces:**
- Produces at the channel boundary: `type PairingConsumer func(context.Context, string, int64) error`.
- Consumes: live sender/chat resolvers, SQLite `ClaimTelegramPairing` / `FinishTelegramPairing`, and `config.LinkTelegramAccount`.

- [ ] **Step 1: Write failing webhook safety tests**

Cover an unmapped private sender pairing through `/start code`, the same code replaying from another sender, expired/invalid codes returning indistinguishable replies, group-chat refusal, ordinary unmapped messages remaining silent/refused, duplicate sender refusal, config mutation failure leaving the pairing unusable only after a successful link, and the newly mapped sender's next message routing to the correct account without restart.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./plugins/channels/telegram ./internal/bootstrap -run 'Pair|Unmapped|Telegram' -count=1`

Expected: FAIL because unmapped senders are currently rejected before command handling.

- [ ] **Step 3: Add one narrow unmapped-sender branch**

After webhook authentication and private-chat validation, parse only Telegram's exact `/start <payload>` form for an unmapped sender. Pass the opaque payload and verified numeric sender to `PairingConsumer`; do not emit an Eggy event, selection, approval, trace, or conversation record. Existing normal-event deduplication occurs downstream, so pairing must use the single-use claim as its own replay protection; repeated deliveries cannot change the mapping twice. Use the same generic failure instruction for invalid and expired codes; log only result class and sender, never the payload.

- [ ] **Step 4: Make resolvers live**

Change `telegramSenders` and `telegramChats` to consume the same config-backed account resolver introduced in Task 4. In the consumer, claim the code, perform the locked validated config mutation, call `FinishTelegramPairing(ctx, claimID, true)` on success, and call `FinishTelegramPairing(ctx, claimID, false)` on mutation failure. Never hold a SQLite transaction while acquiring the config file lock.

Crash contract: a claimed code stays unusable after a crash; never automatically release it on startup, since YAML may already contain the successful link. The owner generates a fresh code after signing in again. If finalization fails after YAML succeeds, report the account as linked based on YAML and leave the claim unusable; do not roll back the link. A pending code may not replace another account's mapping or silently replace this account's existing mapping. Unlink before relinking. Delete pending codes on unlink, removal, and binding reset. In-process pairing generation/claim/link/unlink operations share a narrow mutex owned by the pairing coordinator; config mutations recheck account existence, enablement, and sender uniqueness under the config lock. No database/YAML atomic transaction is claimed.

- [ ] **Step 5: Run channel and integration tests**

Run: `go test ./plugins/channels/telegram ./internal/bootstrap -run 'Pair|Unmapped|Telegram|Account' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit webhook pairing**

```bash
git add plugins/channels/telegram/handler.go plugins/channels/telegram/telegram_test.go internal/bootstrap/telegram.go internal/bootstrap/app.go internal/bootstrap/telegram_pairing_test.go
git commit -m "feat(telegram): pair verified senders at runtime"
```

### Task 9: Update setup and security documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/src/content/docs/get-started/quickstart.md`
- Modify: `docs/src/content/docs/get-started/deploy-railway.md`
- Modify: `docs/src/content/docs/configure/accounts.md`
- Modify: `docs/src/content/docs/use/telegram.md`
- Modify: `docs/src/content/docs/operate/security.md`
- Modify: `docs/src/content/docs/operate/troubleshooting.md`
- Modify: `internal/bootstrap/docs_consistency_test.go`

**Interfaces:**
- Documents the same setup fields, 30-minute setup token, 10-minute Telegram pairing, environment boundary, and headless compatibility path implemented above.

- [ ] **Step 1: Add failing documentation consistency assertions**

Assert the primary quickstart names the setup URL and does not lead with `EGGY_ACCOUNTS`; Railway documents the setup-token log as a credential; Telegram documents Link/Unlink and never recommends `@userinfobot` in the primary path; the headless section still names all compatibility variables.

- [ ] **Step 2: Run the documentation test and confirm failure**

Run: `go test ./internal/bootstrap -run 'Docs' -count=1`

Expected: FAIL against the current environment-variable-first guides.

- [ ] **Step 3: Rewrite the operator journey**

Make the default sequence: provision deployment/sign-in secrets and the selected model key, deploy/build, open the one-time URL from logs, configure the first account, sign in, chat, optionally enable extensions and link Telegram. Document environment secrets in the primary deployment prerequisites; reserve **Headless setup** for noninteractive account/config provisioning. State that bot tokens identify Eggy's bots, Workspace is exclusively Eggy's shared identity, and GitHub/MCP/other extension credentials are optional until configured. State that a setup URL grants configuration authority until exchanged/expired and must not be pasted into support chats.

- [ ] **Step 4: Verify docs and links**

Run: `go test ./internal/bootstrap -run 'Docs' -count=1`

Run: `cd docs && bun run build`

Expected: PASS with no broken internal links or Astro build errors.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md docs/src/content/docs/get-started/quickstart.md docs/src/content/docs/get-started/deploy-railway.md docs/src/content/docs/configure/accounts.md docs/src/content/docs/use/telegram.md docs/src/content/docs/operate/security.md docs/src/content/docs/operate/troubleshooting.md internal/bootstrap/docs_consistency_test.go
git commit -m "docs: make guided setup the default path"
```

### Task 10: Verify the complete fresh-install and upgrade paths

**Files:**
- Create: `internal/bootstrap/setup_integration_test.go`
- Modify: `internal/bootstrap/accounts_integration_test.go`
- Modify: `Makefile` only if a setup smoke target can reuse existing commands without adding a second harness.

**Interfaces:**
- Exercises the public HTTP/config/store/channel seams created in Tasks 1-8; introduces no production interface.

- [ ] **Step 1: Write the end-to-end fresh-home test**

Start with no config and provisioned test environment credentials, exchange a deterministic test token, submit only ordinary configuration and credential variable names, complete setup, and build `App` from the written config and the same environment. Enroll the first Google identity through the existing fake, create a Telegram pairing, post a signed private webhook `/start`, then post a second message and assert its event principal is the first account. Assert `.env` is untouched and no environment credential appears in YAML, HTTP bodies, captured logs, traces, or conversation history. Repeat web-only setup without GitHub, MCP, Workspace, or Telegram credentials and verify those optional capabilities are absent.

- [ ] **Step 2: Add upgrade compatibility cases**

Boot one existing account-mode fixture and one legacy owner fixture unchanged. Boot a fresh headless `EGGY_ACCOUNTS` fixture. Assert none enters setup mode and existing database migrations preserve account ownership.

- [ ] **Step 3: Run focused integration tests**

Run: `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp go test ./internal/bootstrap -run 'Setup|Account|Migration' -count=1`

Expected: PASS.

- [ ] **Step 4: Run the required repository verification**

Run: `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp make fmt vet test race build`

Expected: every target exits 0. Inspect the output rather than treating command completion as proof.

- [ ] **Step 5: Run smoke when Docker is available**

Run: `docker info`

If it exits 0, run: `make smoke`

Expected: PASS. If Docker is unavailable, record smoke as blocked by the environment; do not call it passed.

- [ ] **Step 6: Inspect scope and commit the integration gate**

Run: `git status --short && git diff --check && git diff --stat && git diff --cached --name-only`

Confirm only task-owned files are staged, including database-plan updates only when part of the authorized scope.

```bash
git add internal/bootstrap/setup_integration_test.go internal/bootstrap/accounts_integration_test.go
git commit -m "test: verify guided setup and Telegram pairing"
```


## Detailed acceptance and recovery gates

### Setup lifecycle (Tasks 1-3)

- [ ] Detect a fresh home using both the missing config and absence of prior durable/user artifacts. A missing config beside an existing database or account memory is recovery, never permission to claim the installation. Do not open/migrate SQLite simply to decide freshness; the existing file's presence suffices to refuse setup.
- [ ] Extend `CompleteSetup` to receive the server-resolved home path (not a browser-supplied path). Resolve `data_dir` and runner root from that home; never hardcode `/data` for a local `--home` run. Test explicit home and explicit config path separately.
- [ ] Setup auth uses a 30-minute absolute expiry for both the exchange token and resulting session, Secure cookies on HTTPS, HttpOnly, SameSite=Strict, request-size limits, CSRF checks, and no-store responses. Permit HTTP only on localhost. Derive the public origin from trusted operator settings/Railway, or confirm a validated origin in the authenticated form; never trust arbitrary forwarded headers.
- [ ] Print the initial setup credential only to the operator console, not the persistent application logger. A fragment avoids HTTP access logs but does not make the console URL non-secret. Clear it from browser history before sending the exchange request.
- [ ] The authenticated `POST /api/setup/validate` consumes exactly `SetupInput` and returns field errors plus presence booleans for only the credential names needed by that candidate. Use a shared `ValidateSetup(homePath, input, getenv)` candidate builder from `internal/config`; completion runs it again under the config lock. The endpoint never enumerates the environment.
- [ ] Missing credentials keep setup open. Explain that environment changes require a process/container restart; `/restart` rebuilds App but does not reload the getter captured by `run()`. After process restart, the operator opens the newly issued setup URL.
- [ ] Refuse unknown JSON fields such as `login_client_secret`, `provider_api_key`, or `telegram_bot_token`. Never include a whole input/config/secrets object in an error or failing-test message.
- [ ] Finish is serialized and idempotently reports completion after a successful config write; a second browser cannot overwrite it. If startup later fails, the UI polls mode and displays safe-mode recovery rather than polling forever.
- [ ] Preserve external headless setup and existing config behavior. Setup supplies no personal Workspace grant and makes no outbound OAuth consent request.

### Telegram enablement and compatibility (Tasks 5-8)

- [ ] Represent `telegram.enabled` as `*bool`, not a plain bool: absent preserves existing inference from numeric mappings, explicit false disables, explicit true enables an unpaired bot. Fresh wizard configs omit Telegram until selected; the enable action writes true. Test all three states with legacy and account-mode fixtures.
- [ ] Add the authenticated Settings enable/disable route through `internal/config`, validate token/webhook-secret presence on enable, and retain restart for constructing/removing the adapter. The already-enabled bot's link/unlink operations require no restart.
- [ ] Keep webhook installation explicit: the UI/docs show the deployment's exact HTTPS webhook endpoint and operator instructions using the environment credentials. This plan does not silently add webhook registration or polling. Test with a configured webhook before declaring live pairing usable.
- [ ] Extend the existing Telegram client with `GetMe` only if absent; use an adapter fake HTTP server to verify username discovery and error handling. Discovery failure disables the pairing button with a retry/restart explanation without stopping web chat.
- [ ] Bind pairing initiation to the authenticated principal, never a JSON account ID. A route path may identify only the same account. All ordinary account administration retains the repository's equal-capability model; self-linking is proof of channel possession, not a new administrator role.
- [ ] Add crash tests at claim, config-write, and finalization boundaries. Verify claimed codes never become usable by another sender after restart, failed config mutation releases a claim, and a completed link does not change on webhook replay.
- [ ] Test that unknown private senders still cannot invoke any command except the exact pairing operation, group updates are refused, and linking never invokes generic Telegram selection or payload-bound tool approval logic.

### Scope and verification

- [ ] Production deletion/addition budget: zero tools, zero new secret/configuration environment keys, zero background loops, zero durable formats; one optional `telegram.enabled` YAML field, one pairing table, setup routes present only during fresh setup, and pairing routes present only for a configured Telegram capability. Record actual production line additions/deletions at implementation review; no invented line-count estimate or target.
- [ ] Verify Workspace's existing wrong-identity rejection, sealed grants, and generation-bound approval tests still pass. This plan does not implement Google Cloud service accounts, delegation, impersonation, Discord, or a credential manager.
- [ ] Run frontend tests with the repository's actual `bun test` runner. Inspect desktop/mobile setup and account linking using the browser skill when available; report unavailable browser checks explicitly.
- [ ] Review the final changed-file list before any commit. Each task's command is illustrative of its owned files; include all actual task files and exclude unmodified or unrelated files. No commit or push is authorized by this planning request.


## SQLite storage acceptance

Keep `plugins/store/sqlite` as the persistence adapter and `<home>/eggy.db`
as the single machine-managed database. Bootstrap owns its lifetime; kernel
and ports remain independent of SQL/driver types. No database selector,
connection URL, PostgreSQL service, or new store abstraction is needed for setup.

Eggy's existing implementation already uses WAL mode, FTS5 message search,
and a single database connection. Preserve those settings and the existing
schema; the Hermes excerpt is a reference for the storage pattern, not a
schema to transplant. Conversations/threads, message history, traces, state,
approvals, schedules, sealed grants, sessions and identity bindings retain their
existing tables. Add only the expiring Telegram pairing record required here.

- [ ] Verify a new account's messages survive restart and remain isolated from
  another account in history and search.
- [ ] Verify ordinary App restart drains work before closing its database;
  setup never opens a second runtime database handle.
- [ ] Keep durable messages filtered for active secrets and transient image
  bytes. Do not introduce raw provider request sidecars or copy Hermes's
  provider-specific reasoning columns.
- [ ] Preserve existing compaction/history behavior. Profiles, per-profile
  database files, subagents, extra FTS tokenizers, retries, periodic checkpoints,
  and session lineage changes require their own demonstrated need.
- [ ] Document backup of the complete persistent home with a safely closed
  database or a SQLite-aware backup, including WAL considerations and the
  separately protected encryption key.
