# Eggy Subaccounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Use superpowers:subagent-driven-development only if the user requests delegation. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give two designated people isolated Eggy accounts while both use Eggy's own shared Google Workspace identity.

**Architecture:** Resolve a provider-neutral account principal at ingress and carry it through the existing runtime and storage paths. Keep one App, event loop, scheduler, database, and shared outbound Google OAuth grant. Add a separate inbound Google OIDC adapter and SQLite-backed browser sessions.

**Tech Stack:** Go 1.26, net/http, existing SQLite/Markdown adapters, golang.org/x/oauth2, a maintained Go OIDC verifier, React/TypeScript/Bun.

**Spec:** `docs/superpowers/specs/2026-09-12-subaccounts-design.md` — read before execution. Defaults in that document are explicit planning choices; this plan does not authorize deployment or Google account provisioning.

## Global Constraints

- Go 1.26; standard library HTTP and existing ports/adapters architecture.
- One process, one replica, one event loop, one scheduler, one SQLite database.
- YAML startup configuration, Markdown owner documents, SQLite machine records only.
- Zero new model tools and zero new background loops.
- Register through bootstrap; config mutation stays under `internal/config` and its existing lock/validation.
- Preserve approval mechanism, secret filtering, atomic Markdown writes, webhook authentication, allowlisting, and update deduplication.
- No domain-wide delegation, Google Cloud service-account implementation, personal Google API grants, public registration, general RBAC framework, or per-account App instances.
- Every behavior change starts with a focused failing test. Finish each task's focused tests before advancing. Commit checkpoints are available only when the user authorizes commits; stage task-owned files only.

## Execution order and repository map

Execute tasks sequentially. Tasks 1–4 establish boundaries; 5–7 establish authentication and shared Google identity; 8–10 integrate surfaces and scheduling; 11 verifies and documents the release. Do not expose member login on a deployed instance until all isolation tests pass.

Existing implementation anchors:

| Responsibility | Existing files |
|---|---|
| Config and first boot | `internal/config/config.go`, `config_validate.go`, `config_init.go`, `config_mutate.go` |
| Database schema and private stores | `plugins/store/sqlite/machine.go`, `store.go`, `state.go`, `traces.go`, `schedules.go` |
| Owner documents | `internal/home/home.go`, `plugins/context/markdown/store.go` |
| Event identity and routing | `internal/kernel/events/events.go`, `internal/kernel/services/dispatcher.go`, `internal/bootstrap/app_events.go` |
| Composition | `internal/bootstrap/app.go`, `app_wiring.go`, `telegram.go`, `google.go` |
| Authentication and HTTP | `plugins/auth/session/cookie.go`, `internal/web/login.go`, `web.go`, `safemode.go` |
| Shared grant | `plugins/tools/google/oauth.go`, `store.go`, `internal/commands/google.go` |
| Browser | `website/src/LoginPage.tsx`, `App.tsx`, `api.ts`, `GoogleCard.tsx` |

New files are named in their tasks. Keep provider types out of kernel/ports. Extend existing files for existing behavior; do not reorganize bootstrap or introduce lifecycle factories.

## Task 1: Define account configuration and a trusted principal

**Files:** Create `internal/ports/account.go`, `internal/config/accounts.go`, `internal/config/accounts_test.go`; modify `internal/config/config.go`, `config_validate.go`, `config_init.go`, `config_mutate.go` and their existing tests.

**Interfaces:** Define these provider-neutral contracts in ports. Account resolution is a read-only closure over validated config, wired by bootstrap; roles are always resolved from current configuration, not session claims.

```go
type Principal struct { AccountID string; Admin bool }
func WithPrincipal(ctx context.Context, p Principal) context.Context
func PrincipalFromContext(ctx context.Context) (Principal, error)
```

Configuration shape (illustrative addresses are operator inputs, not credentials):

```yaml
accounts:
  - id: nigel
    role: admin
    google_email: nigel@example.com
    telegram_user_id: 123456789
  - id: partner
    role: member
    google_email: partner@example.com
web:
  google_login:
    client_id: web-client.apps.googleusercontent.com
    client_secret_env: EGGY_GOOGLE_LOGIN_CLIENT_SECRET
google:
  expected_email: eggy@example.com
migration_owner_id: nigel
```

- [ ] Add table-driven rejection tests for duplicate IDs/emails/Telegram IDs, unsafe path IDs, missing admin, unknown roles, missing login credentials, conflicting legacy owner/account fields, and invalid migration owner. Allow one or more configured accounts; the deployment uses two, without hardcoding a two-user limit.
- [ ] Run `go test ./internal/config ./internal/ports -run 'Account|Principal' -count=1`; expect the new behavior tests to fail before implementation.
- [ ] Implement account IDs with the existing bounded identifier conventions, exact email comparison after trimming/case normalization, and positive optional Telegram IDs. Normalize legacy owner config into one principal; explicit accounts require OIDC and disallow legacy login settings. Add the login secret to `Secrets.Values()` and its reflection coverage.
- [ ] Test absent principals fail rather than resolve to the administrator:

```go
func TestPrincipalRequired(t *testing.T) {
    if _, err := ports.PrincipalFromContext(context.Background()); err == nil {
        t.Fatal("missing account must fail closed")
    }
}
```

- [ ] Rerun focused tests. Verify account configuration writes use the existing config lock/validation and changing a bound email requires explicit re-enrollment, not automatic reassignment.

## Task 2: Migrate historical ownership and scope the SQLite adapters

**Files:** Create `plugins/store/sqlite/accounts_migration.go`, `accounts_migration_test.go`, `account_isolation_test.go`; modify `machine.go`, `store.go`, `state.go`, `traces.go`, `schedules.go`; modify `internal/bootstrap/migration_test.go` and the existing database-opening path in `app_wiring.go`.

**Interfaces:** All existing private store operations consume `ports.PrincipalFromContext(ctx)`. Add `func (s *Store) MigrateAccounts(ctx context.Context, legacyOwnerID string) error` for bootstrap's explicit migration phase. It is not a public HTTP or model tool. Shared grant operations retain their existing interfaces and global keys.

- [ ] Build a version-6 fixture containing messages, an FTS hit, thread/reset/workspace metadata, traces/spans, approvals, schedules, proactive history, runtime modes, and Google/MCP ciphertext. Assert migration assigns private records only to the specified administrator and preserves grant bytes and primary IDs.
- [ ] Run `go test ./plugins/store/sqlite -run 'Account|Migration' -count=1`; verify failure is missing ownership/migration behavior.
- [ ] Add `account_id` ownership and indexes. Replace singleton machine-state key with an account key and per-account version. Scope every read/update/delete, FTS join, aggregate, retention operation, thread workspace lookup, and trace-span lookup. Use bound SQL parameters, including for ownership. Representative predicate:

```sql
SELECT id, title, channel, created_at, updated_at
FROM threads WHERE account_id = ? AND id = ?;
```

- [ ] Upgrade schema transactionally and stamp version 7 only after completion (use the next version if repository state has advanced). Persist the legacy mapping in `schema_meta`; conflicting retry mappings fail. Mark old pending approvals invalidated. Retain source-qualified webhook deduplication so replayed legacy updates are not newly delivered.
- [ ] Add the minimum isolation regression using existing APIs:

```go
func TestAccountCannotReadOtherThread(t *testing.T) {
    db, err := sqlite.Open(filepath.Join(t.TempDir(), "eggy.db"))
    if err != nil { t.Fatal(err) }
    defer db.Close()
    a := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "a"})
    b := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "b"})
    if _, err := db.CreateThread(a, "private-a", "web", time.Now()); err != nil { t.Fatal(err) }
    if _, found, err := db.GetThread(b, "private-a"); err != nil || found {
        t.Fatalf("cross-account lookup: found=%v err=%v", found, err)
    }
}
```

- [ ] Add missing-principal, cross-account rename/delete/search/reset/span tests; rerun the focused suite and `go test ./plugins/store/sqlite -count=1`. Ensure fresh databases work without a historical migration and old binaries reject the upgraded database.

## Task 3: Isolate Markdown context and make migration resumable

**Files:** Modify `internal/home/home.go`, `home_test.go`, `plugins/context/markdown/store.go`, `store_test.go`, `internal/bootstrap/app_wiring.go`, `migration_test.go`.

**Interfaces:** Add `func (l Layout) AccountMemories(accountID string) (string, error)`. It returns `<home>/accounts/<id>/memories` after ID validation. The Markdown adapter uses the principal to resolve USER/MEMORY/WATCH; shared SOUL remains at its existing path.

- [ ] Write tests where A writes memory and B loads empty memory; shared SOUL is identical; invalid IDs and symlink escapes cannot redirect writes. Add restart-after-copy and conflicting-destination migration tests.
- [ ] Run `go test ./internal/home ./plugins/context/markdown ./internal/bootstrap -run 'Account|Migration' -count=1` and confirm expected failure.
- [ ] Resolve private paths from the principal on each call; preserve size limits, file locks, secret guards, and atomic writes. Implement the migration sequence:

```text
record phase in SQLite -> copy to temporary account path -> verify bytes
-> atomic publish -> record copied phase -> archive original -> record complete
```

- [ ] Refuse nonidentical existing destinations; allow byte-identical retries. Keep traffic disabled until the database and document phases both complete. Start the new member with blank documents rather than copying administrator memory.
- [ ] Rerun focused tests and inspect fixtures to verify originals remain recoverable and no fourth durable record format was introduced.

## Task 4: Carry identity through turns and enforce permissions before approvals

**Files:** Modify `internal/kernel/services/dispatcher.go`, `turns.go`, `approval_gate.go`, `approval_service.go`, `internal/kernel/approvals/approvals.go`, `internal/kernel/turns/turns.go`, `internal/bootstrap/app.go`, `app_events.go`, `app_wiring.go`, `internal/commands/commands.go` and corresponding tests.

**Interfaces:** Retain `events.Event.Owner` as the account ID. Dispatcher takes a validated account resolver instead of a singleton owner string. Add `AccountID` and `IntegrationGeneration` to protected approval data. Existing `approvals.Action` and `services.ApprovalToolExecutor` remain the only approval execution path.

- [ ] Test unknown/removed event owners, cross-account approval decisions, altered integration generation, and member administrator operations in each approval mode (`normal`, `strict`, `auto`). Verify denied calls never reach an executor.
- [ ] Run `go test ./internal/kernel/... ./internal/bootstrap ./internal/commands -run 'Account|Permission|Approval' -count=1`; confirm failing cases.
- [ ] Resolve and inject the principal before invoking handlers. Require it through private services, active-turn/steering keys, and model preference selection. Remove singleton owner authorization and any implicit administrator fallback.
- [ ] Apply this ordering in native, command, and remote tool paths:

```text
resolve active account -> authorize capability and resource ownership
-> apply that account's approval mode -> bind approval if required
-> recheck authorization and integration generation -> execute
```

- [ ] Filter administrator-only repository/MCP/skill-write capabilities from member catalogs and enforce the same rule at execution. Do not grant access merely because a tool name was once listed. Retain one registry and inline bootstrap registration; use account-aware filtering of that registry, not duplicated catalogs.
- [ ] Rerun focused tests. Verify secret guards cover both login credentials and shared Google credentials, and member `auto` never authorizes administration.

## Task 5: Implement revocable sessions and single-use login transactions

**Files:** Create `plugins/store/sqlite/sessions.go`, `sessions_test.go`; modify `plugins/auth/session/cookie.go`, `cookie_test.go`, `internal/web/login.go`, `web_test.go`.

**Interfaces:** SQLite exposes the following contracts to the session adapter/web boundary; `expiresAt` and `now` are explicit for tests. Raw tokens stay only in browser cookies and short-lived local variables.

```go
func (s *Store) CreateSession(ctx context.Context, hash, accountID string, expiresAt time.Time) error
func (s *Store) SessionAccount(ctx context.Context, hash string, now time.Time) (string, error)
func (s *Store) RevokeSession(ctx context.Context, hash string) error
func (s *Store) RevokeAccountSessions(ctx context.Context, accountID string) error
```

- [ ] Add tests for expired/revoked sessions, token hashing, account removal, cookie flags, logout CSRF, old expiry-only token rejection in account mode, transaction replay, and browser-binding mismatch.
- [ ] Run `go test ./plugins/auth/session ./plugins/store/sqlite ./internal/web -run 'Session|LoginTransaction|CSRF' -count=1` and confirm new failures.
- [ ] Generate session tokens using `crypto/rand` with 32 random bytes, URL-safe encode, and hash with SHA-256 for storage. Resolve current account/role from config on each request. Reject inactive accounts and revoke their sessions.
- [ ] Add a login transaction row with state hash, browser-binding hash, nonce, sealed PKCE verifier, and five-minute expiry. Consume atomically on callback; prune expired transactions/sessions during auth operations without a goroutine. Cookies use the spec's flags and local fixed redirects.
- [ ] Implement same-origin/CSRF checks for mutating authenticated HTTP endpoints and account-scoped logout. Retain legacy authentication only for normalized legacy configuration; it still resolves an explicit administrator principal.
- [ ] Rerun focused tests; inspect database fixtures to confirm raw session tokens are absent.

## Task 6: Add allowlisted Google Sign-In and identity binding

**Files:** Create `plugins/auth/google/oidc.go`, `oidc_test.go`, `plugins/store/sqlite/identities.go`, `identities_test.go`, `internal/web/google_login.go`, `google_login_test.go`; modify `go.mod`, `go.sum`, `internal/bootstrap/app.go`, `internal/web/web.go`.

**Interfaces:** The inbound adapter exposes `Begin(ctx, state, nonce, verifier string) (string, error)` and `Complete(ctx, code, nonce, verifier string) (Identity, error)` on its `Client`; `Identity` contains `Issuer`, `Subject`, `Email` strings and `EmailVerified bool`. Only bootstrap/web identity handling consumes these provider types. SQLite binding method: `BindIdentity(ctx context.Context, accountID, issuer, subject string) error`, with uniqueness on account and issuer/subject.

- [ ] Add fake-provider/JWKS tests for valid login, wrong audience/issuer/signature/nonce, expired token, unverified email, unlisted user, duplicate subject binding, and concurrent first login. Test changed email cannot replace an existing subject.
- [ ] Run `go test ./plugins/auth/google ./plugins/store/sqlite ./internal/web -run 'OIDC|Identity|GoogleLogin' -count=1`; new files/contracts fail until implemented.
- [ ] Add a maintained OIDC verifier (evaluate `github.com/coreos/go-oidc/v3/oidc`, pin a compatible reviewed version during execution) plus existing oauth2. Use Google's fixed issuer and server-side callback derived from validated public base URL. Test endpoints are package-local injection, never operator-settable token hosts.
- [ ] Wire `GET /auth/google/start` and `GET /auth/google/callback`. Scope is `openid email profile`; callback atomically consumes the login transaction, validates identity, binds only an exact allowlisted unbound account, and issues an Eggy session. Identity token verification is not deferred to browser code.
- [ ] Test callback failures produce a generic login error, no session, no token/code logging, and no open redirect. Ensure disabling Google login constructs no provider client or routes in legacy mode.
- [ ] Rerun focused tests with fake HTTP only; no live credentials required.

## Task 7: Verify and expose Eggy's shared Google identity

**Files:** Modify `plugins/tools/google/oauth.go`, `store.go`, `google_test.go`, `internal/bootstrap/google.go`, `google_test.go`, `internal/config/config.go`, `config_validate.go`, `internal/commands/google.go`, `google_test.go`.

**Interfaces:** Extend the existing `TokenRecord` with verified `Email`, `Subject`, and `Generation`; add expected email to the existing Google adapter config. Keep `google/workspace`, the existing sealer associated data, and all product tool APIs unchanged.

- [ ] Test an administrator connecting the expected Eggy identity succeeds, connecting a personal identity fails without overwriting the current grant, member connection commands fail, and a grant lacking identity metadata cannot serve member tools until verified.
- [ ] Run `go test ./plugins/tools/google ./internal/bootstrap ./internal/commands -run 'Google|Identity|Generation' -count=1`; confirm new negative cases fail.
- [ ] Request identity scopes alongside configured product scopes and verify account metadata through Google's fixed identity endpoint before storing a replacement grant. This outbound identity check stays in `plugins/tools/google`; do not unify inbound/outbound OAuth implementations.
- [ ] Preserve consent, refresh token, granted scopes, PKCE, pending window, exact loopback redirect, fixed token endpoint, and record sealing. Increment generation on connection replacement/disconnect; reject approvals from earlier generations even if the new account's email matches.
- [ ] Return only verified email, shared status, products, and connection state to members. Keep authorization URLs/grant-management controls administrator-only. Do not create a separate Google grant for either human user.
- [ ] Rerun focused tests, including missing-refresh-token and revoked-grant cases; verify Google disabled still registers no tools or grant store.

## Task 8: Scope HTTP, live streams, Telegram, and recovery

**Files:** Modify `internal/web/web.go`, `chat.go`, `traces.go`, `approvals.go`, `schedules.go`, `watch.go`, `safemode.go`, corresponding tests, `plugins/channels/webchat/hub.go`, `channel.go`, their tests, `plugins/channels/telegram/handler.go`, `client.go`, their tests, `internal/bootstrap/telegram.go`, `routed_channel.go`.

**Interfaces:** Extend hub registration/broadcast to `(accountID, threadID string, ...)`; do not accept account ID from browser parameters. Telegram handler maps verified numeric sender ID to configured account ID. Notification destinations include the resolved account, with no singleton fallback.

- [ ] Add a route matrix for member reads/writes against another account's thread/history/trace/span/approval/schedule/watch and every administrator route. Include direct URL guessing, forged JSON owner fields, SSE subscription, and stream revocation.
- [ ] Run `go test ./internal/web ./plugins/channels/... ./internal/bootstrap -run 'Account|Session|Stream|Telegram|SafeMode' -count=1`; confirm failures.
- [ ] Replace `WebUIConfig.OwnerID` use with authenticated request identity. Require ownership before lookup/mutation/stream registration; cross-account resources return not-found without metadata. Use context cancellation or existing delivery hooks to close revoked sessions' streams, without a periodic auth loop.
- [ ] Resolve Telegram private-chat sender IDs into accounts; preserve secret authentication and deduplication. Reject unmapped senders and group delivery. `/web` in account mode sends the panel URL, never a signed login link. Route approvals/typing/edits/replies only to the owning account.
- [ ] Classify all config/restart/provider/MCP/raw-status HTTP and command paths as administrator-only server-side. In account-mode safe mode, authenticated admin recovery requires valid identity config/database; otherwise require host YAML repair. Never fall back to global password auth. Retain restart preflight/draining.
- [ ] Rerun focused tests and assert no member path receives raw global config or another user's prompt/trace contents.

## Task 9: Route scheduled work and isolate active turns

**Files:** Modify `plugins/scheduler/local/scheduler.go`, `scheduler_test.go`, `internal/kernel/services/schedule_tools.go`, `turns.go`, their tests, `internal/bootstrap/app_events.go`, `heartbeat_test.go`, `steering_test.go`, `internal/kernel/destination/destination.go`.

**Interfaces:** Schedule persistence retains account ownership; scheduler emits `Event.Owner` from the stored schedule. Active-turn lookup uses `(accountID, conversationID)`. Private state/preferences are loaded under that principal.

- [ ] Test two accounts with overlapping conversation identifiers, cancellation/steering attempts across accounts, independent approval modes/model settings, account removal before a schedule fires, and per-account notification recipients.
- [ ] Run `go test ./plugins/scheduler/local ./internal/kernel/services ./internal/bootstrap -run 'Account|Schedule|Heartbeat|Steering' -count=1` and verify failing new cases.
- [ ] Iterate due schedules in the existing scheduler; restore the owning principal before dispatch. Disabled/deleted accounts do not run. Heartbeat uses the existing ticker and per-account watch/preferences, with no extra scheduler or event loop.
- [ ] Preserve the existing read-only/no-MCP restrictions for unprompted agent turns. Web-only accounts retain scheduled output in their private history; do not deliver it to another user's Telegram when no recipient exists.
- [ ] Rerun focused tests and race-sensitive active-turn tests; verify no global mutable “current account” was introduced.

## Task 10: Add account-aware browser behavior

**Files:** Modify `website/src/LoginPage.tsx`, `App.tsx`, `api.ts`, `GoogleCard.tsx`, `ConfigPage.tsx`, `SafeModePage.tsx`; create `website/tests/accounts.test.ts`; modify existing relevant tests.

**Interfaces:** Authenticated session response exposes `{ account: { id, role }, google: { email, shared: true } }` without tokens. The client does not supply account selection on private API calls. Member Google connection status is separate from administrator configuration editing.

- [ ] Write browser tests for Sign in with Google, denied login, member navigation, verified shared email display, sign-out/session expiry, and clearing user A's cached state before user B signs in.
- [ ] Run `cd website && bun run test`; confirm new account behavior assertions fail.
- [ ] Replace password form in account mode with an ordinary link to `/auth/google/start`. Show current user and logout, hide administrator navigation for members, and show “Shared with both Eggy users” beside the verified Google identity. Keep server permission checks authoritative.
- [ ] On logout or 401, close streams and clear thread, trace, approval, and config state. Include CSRF token headers in mutating requests. Preserve existing responsive layout and recognizable controls.
- [ ] Run `cd website && bun run test && bun run build`. Use the browser skill for an actual two-session UI check when browser tooling is available; record unavailable checks as blocked.

## Task 11: Verify migration, isolation, and operational setup

**Files:** Create `internal/bootstrap/accounts_integration_test.go`; update `AGENTS.md`, `config.example.yaml`, `docs/src/content/docs/configure/configuration.md`, `configure/google-workspace.md`, `operate/security.md`, `operate/persistence-memory.md`, `operate/safe-mode.md`, `use/telegram.md`, `use/approvals.md`, `use/web-chat.md`. Follow `docs/AGENTS.md` for documentation-site changes.

- [ ] Add an end-to-end fake-adapter scenario: migrate an existing owner home, log in A and B through fake OIDC, exchange private messages, access the same fake Google mailbox, reject cross-account reads/approvals/streams, revoke B, and restart. Assert shared grants survive and private content never appears in the other's prompt or trace.
- [ ] Run `go test ./internal/bootstrap -run 'Account|Migration' -count=1`; fix any missing integration behavior and rerun until passing.
- [ ] Document the operator sequence: back up stopped home; provision Eggy's Workspace user/mailbox; configure separate Web sign-in and existing Desktop Google OAuth clients; set exact callback/loopback URIs; configure the two allowlisted emails and optional Telegram IDs; set expected Eggy email; choose historical administrator mapping; start migration; sign in; authorize Google as Eggy; share calendars/files or forward mail; verify both accounts.
- [ ] Document OAuth consent-screen publishing constraints, external versus internal audiences, revocation/reconnect, host recovery, and full-backup rollback. Clarify that personal Gmail delegation is not implemented and forwarding into Eggy's inbox makes the message shared with both users.
- [ ] Update AGENTS.md's single-owner and per-sender isolation decisions to reflect this explicit product change. Keep declined subagents/channel breadth/profile capabilities declined. Record private memory's `InternalTool` reasoning and account-scoped `/mode` semantics.
- [ ] Run required checks from repository root:

```sh
make fmt vet test race build
```

- [ ] If sandbox caches are unavailable, rerun with `GOCACHE=/tmp/eggy-go-cache GOTMPDIR=/tmp` and record the environment adjustment. Run `make smoke` when Docker is available; report unavailable daemon as blocked, not passed. Run the documentation site's defined build when modifying published docs.
- [ ] Review `git diff --check`, `git diff --stat`, and the full task-owned diff. Report passed checks and any blockers. Do not deploy, provision external resources, or commit/push without session authorization.

## Plan review checklist

- [ ] Config and migration map each historical record to one explicit account.
- [ ] Missing identity fails closed across every private store and service.
- [ ] HTTP, Telegram, tools, scheduled work, streams, cancellation, and approvals use the same principal.
- [ ] Authorization precedes approval mode, including `auto`.
- [ ] Shared Google grant is identity-verified, generation-bound, and managed only by the administrator.
- [ ] Member login is not enabled before isolation, migration, and recovery paths pass.
- [ ] Existing secrets, OAuth invariants, zero-cost disabled capabilities, and single-process boundaries remain intact.

## Handoff

This is a plan, not an implementation or a claim of passing tests. Execute inline with `superpowers:executing-plans` after the user requests implementation. Keep the spec and plan together; revise both if a planning default changes.
