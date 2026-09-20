# Local accounts and Telegram web access implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking. Recommended execution: native, sequential implementation; these tasks share authentication and migration contracts.

**Goal:** Give trusted users private username/password accounts and single-use Telegram `/web` login links, sharing one Eggy runtime and provider configuration.

**Architecture:** Keep account membership in YAML and machine-managed credentials, login links, and sessions in the existing SQLite database. Both login methods create the existing account session and use the existing ownership/CSRF guard. Remove inbound Google Sign-In after explicit migration; retain outbound Workspace OAuth unchanged.

**Tech Stack:** Go 1.26 standard library, existing modernc SQLite adapter, React/TypeScript, Bun tests; no new dependency.

**Spec:** [Approved design](../specs/2026-09-20-password-account-onboarding-design.md).

## Global Constraints

- All trusted accounts can manage People; the first account is not a separate administrator role.
- Keep account membership and Telegram IDs in YAML under the existing config mutation lock.
- Keep machine-managed credentials and web links in SQLite.
- Never store raw passwords in YAML, logs, traces, or durable conversation context.
- No local password record is allowed for the environment-bound account, avoiding two password authorities for one account.
- Do not migrate outbound Google authentication in this work.
- Tools: zero. Background loops: zero. Frameworks, databases, processes, replicas, and roles: zero new.
- Explicit SQLite migration and machine-state version bump are required.
- Go 1.26; one `eggyd` process and one replica. No provider-specific dependencies in kernel or ports.
- Preserve existing user records and account IDs; breaking config/schema migrations are authorized, data loss is not.
- No deployment, live credential edits, commits, or pushes are authorized by this planning request. Commit steps below apply during approved implementation.

## Review Focus

1. A local password reset racing a successful verification must prevent the old password creating a new session afterward (Task 2 generation compare-and-insert; Task 4 HTTP regression).
2. A crash between YAML removal and SQLite cleanup must not let the same username inherit old private records (Tasks 2 and 5 retained account-auth rows and admission refusal).
3. Telegram previews, browser prefetches, and an already signed-in browser must not consume a link or silently switch accounts (Tasks 6 and 7 explicit redemption POST).
4. Queued Telegram input after sender reassignment must not mint a link as either the old owner or the newly linked sender (Task 6 verified sender metadata and current mapping check).
5. Migration interrupted between database and config writes must resume without deleting newly issued sessions on every restart or losing outbound OAuth grants (Task 3 staged cutover and restart tests).

## Scope decisions and file ownership

This is one authentication change with nine sequential deliverables, not nine separately deployable releases. Keep intermediate commits local until the full cutover passes.

| Responsibility | Files |
|---|---|
| Password hashing | New `plugins/auth/session/password.go`, `password_test.go` |
| Neutral auth records/store contract | New `internal/ports/account_auth.go` |
| Credential/link SQL and migration | New `plugins/store/sqlite/account_auth.go`, `account_auth_test.go`; existing `store.go`, `machine.go`, `sessions.go`, migration tests |
| Config migration/identity | New `internal/config/local_accounts.go`, `local_accounts_test.go`; existing `accounts.go`, `config.go`, `config_init.go`, `config_mutate.go`, `config_validate.go`, `setup.go` |
| Offline cutover command | New `cmd/eggyd/migrate_login.go`, `migrate_login_test.go`; existing `cmd/eggyd/main.go` |
| Login and recovery | New `internal/web/account_auth.go`, `account_auth_test.go`; existing `login.go`, `session.go`, `web.go`, `safemode.go`; `internal/bootstrap/login.go`, `app.go`, `account_directory.go` |
| People/password management | Existing `internal/web/accounts.go`, `account_routes_test.go`, `accounts_test.go`, config tests |
| Verified Telegram link minting | Existing `plugins/channels/telegram/handler.go`, `internal/kernel/events/events.go`, `internal/commands/commands.go`, `internal/bootstrap/app_events.go`; new `internal/commands/web.go`, tests |
| Link redemption | New `internal/web/login_link.go`, `login_link_test.go` |
| Browser flows | Existing `website/src/AccountsCard.tsx`, `LoginPage.tsx`, `SetupPage.tsx`, `api.ts`, `App.tsx`; new `WebLoginLinkPage.tsx`; `website/tests/` |
| Personal settings | Existing account runtime services and routes; new `website/src/PersonalSettingsCard.tsx` and test |
| Removal/docs/integration | Inbound Google-only files listed in Task 9; existing bootstrap integration tests, docs, `AGENTS.md`, `.env.example` |

Do not edit the pre-existing work in `website/src/ChatPage.tsx` or `website/tests/reply-selection.test.ts`. At execution start inspect `git status --short`, `git diff`, and `git diff --cached --name-only`; isolate work using the worktree skill if needed.

## Shared implementation contracts

### Password format

Use standard-library PBKDF2-HMAC-SHA256: 600,000 iterations, 16 random salt bytes, 32 derived bytes. Format: `eggy-pbkdf2-sha256-v1$600000$<raw-base64-salt>$<raw-base64-key>`. Only that version and parameter set are accepted; untrusted stored parameters must not choose arbitrary work factors. New passwords: 12–256 UTF-8 bytes, valid UTF-8, no trimming or Unicode normalization. Existing environment passwords are not silently changed or rejected merely for being shorter; retain their exact value, enforce the same 256-byte request bound, and report an overlong configured secret during preflight.

This selects PBKDF2 for the repo's standard-library preference, not as a claim that it is memory-hard. API and work-factor references: [Go PBKDF2](https://pkg.go.dev/crypto/pbkdf2), [OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html). Verify `go doc crypto/pbkdf2.Key` on the execution toolchain. No module upgrade is needed.

### Neutral auth types (Task 2 creates these in `internal/ports/account_auth.go`)

```go
var ErrAuthDenied = errors.New("authentication denied")
var ErrAccountIDUsed = errors.New("account id has already been used")

type AccountAuth struct {
    AccountID string
    PasswordHash string // empty for pending or environment-backed accounts
    Generation int64
    Retired bool
}

type WebLoginLink struct {
    AccountID string
    SenderID string // provider-neutral text; minting ingress validates it
    Generation int64
    ExpiresAt time.Time
}

type AccountAuthStore interface {
    RegisterAccountAuth(ctx context.Context, accountID string) error
    AccountAuth(ctx context.Context, accountID string) (AccountAuth, error)
    SetAccountPassword(ctx context.Context, accountID, encoded string, expectedGeneration int64) error
    RetireAccountAuth(ctx context.Context, accountID string) error
    RevokeAccountAuth(ctx context.Context, accountID string) error
    CreateAuthenticatedSession(ctx context.Context, hash, accountID string, generation int64, expiresAt time.Time) error
    CreateWebLoginLink(ctx context.Context, hash string, link WebLoginLink, now time.Time) error
    WebLoginLink(ctx context.Context, hash string, now time.Time) (WebLoginLink, error)
    RedeemWebLoginLink(ctx context.Context, linkHash, sessionHash string, expected WebLoginLink, now, sessionExpiry time.Time) error
}
```

`RegisterAccountAuth` inserts exactly once, generation 1; an existing row is `ErrAccountIDUsed`, including pending/retired rows. The row is a credential/lifecycle record, not a second membership directory. Membership remains YAML. Never automatically reactivate a retired row. Account IDs retain their existing spelling; uniqueness uses case-insensitive comparisons, but authentication accepts the stored exact ID after surrounding whitespace removal. Reserve environment alias comparisons case-insensitively to avoid ambiguity.

`SetAccountPassword` increments generation, replaces the hash, and deletes sessions/links atomically, conditional on the supplied generation and not retired. `RevokeAccountAuth` increments generation and deletes sessions/links without changing the password. `RetireAccountAuth` also clears the hash and sets retired. Local password login uses `CreateAuthenticatedSession` conditional on the generation that was verified. This is necessary; checking a password and then unconditionally calling `CreateSession` races a reset.

### Config/read locking (Task 3 creates these)

```go
// internal/config/local_accounts.go
func WithAccount(path, id string, fn func(Config, AccountConfig) error) error
func MigrateLocalAccounts(path, passwordAccountID string, getenv func(string) string) error
```

`WithAccount` holds the existing `filelock.With(path, ...)`, reads and validates the current document, resolves the account, and invokes `fn`. Auth completion, link mint/redemption, and credential mutations use it. It is a read operation and cannot call config mutation functions inside its callback. Lock order: config file lock before a short SQLite transaction, never SQLite before config; do not perform password hashing or outbound HTTP under either lock. `internal/config` never imports web, bootstrap, or a store. Test configurations with no path use the validated initial config through the same web-side helper.

### Wire contract

| Route | Request | Result |
|---|---|---|
| `POST /api/login` | `{ "username": "partner", "password": "…" }` | Normal account cookie; then fetch `/api/session` for identity/CSRF |
| `POST /api/config/accounts` | `{ "id": "partner", "telegram_user_id": 123, "password": "…" }` | Membership and credential readiness; pending state explicitly reported on credential failure |
| `POST /api/config/accounts/{id}/password` | `{ "password": "…", "current_password": "…" }` | Own change requires current password; another trusted user's reset does not |
| `POST /api/config/accounts/{id}/revoke-sessions` | `{}` | Generation increment; all sessions and links invalidated |
| `POST /api/login/link` | `{ "token": "…" }` | Atomically consume link and create account session |
| `GET /auth/link#token=…` | Fragment never reaches HTTP server | Static confirmation page; no authentication side effect |

Login accepts legacy `email` only as a temporary request alias during frontend commit sequencing, rejects conflicting `username`/`email`, and removes the alias in Task 9. Account list exposes `password_state: "environment" | "set" | "pending"`, `web_link_available`, stable ID, and existing channel status, never hashes or passwords. All management writes require an existing account session and CSRF. Auth input bodies are capped at 4 KiB, decoded strictly, and never echoed in errors. Unsupported methods return 405; credential failure is generic 401; malformed input 400; store outage 503. The creation partial-failure response is 503 with `account_created: true` and no secret-bearing detail.

## Task 1: Versioned password hashing

**Files:** Create `plugins/auth/session/password.go`, `password_test.go`.
**Consumes:** Go 1.26 crypto/rand, crypto/pbkdf2, crypto/sha256, crypto/subtle.
**Produces:** `HashPassword(password string) (string, error)`, `VerifyPassword(encoded, password string) bool`, `ValidatePassword(password string) error` in package session.

- [ ] Write the failing test below and table cases for 11/12/256/257 bytes, malformed base64, wrong algorithm/version/iteration count, embedded `$`, invalid UTF-8, and whitespace preserved.

```go
func TestPasswordHashRoundTripAndSalt(t *testing.T) {
    const password = "a sufficiently long password"
    first, err := HashPassword(password)
    if err != nil { t.Fatal(err) }
    second, err := HashPassword(password)
    if err != nil { t.Fatal(err) }
    if first == second { t.Fatal("salt reused") }
    if !VerifyPassword(first, password) || VerifyPassword(first, password+"x") {
        t.Fatal("incorrect password verification")
    }
    if VerifyPassword("eggy-pbkdf2-sha256-v1$999999999$x$y", password) {
        t.Fatal("unbounded work factor accepted")
    }
}
```

- [ ] Run `go test ./plugins/auth/session -run 'TestPassword' -count=1`; expect missing functions before implementation.
- [ ] Implement the fixed format, bounds, and strict decoder. Hashing core:

```go
salt := make([]byte, 16)
if _, err := rand.Read(salt); err != nil { return "", err }
key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
if err != nil { return "", err }
return "eggy-pbkdf2-sha256-v1$600000$" +
    base64.RawStdEncoding.EncodeToString(salt) + "$" +
    base64.RawStdEncoding.EncodeToString(key), nil
```

Verification checks total encoded length, exactly four segments, version, literal iteration string, and decoded lengths before deriving a key; use `subtle.ConstantTimeCompare`. Never trim passwords. Generate one valid dummy hash at app construction for unknown-user checks, not on each request.

- [ ] Run the focused tests and `go test ./plugins/auth/session`. Add a benchmark for verification and record actual duration; do not silently reduce the work factor. Reuse the existing throttle and cap concurrent password verifications at two in Task 4, returning 429 when full rather than spawning goroutines.
- [ ] Review only these files, stage them explicitly, commit `feat(auth): add versioned local password hashing`.

## Task 2: SQLite auth records, generation checks, and single-use links

**Files:** Create `internal/ports/account_auth.go`, `plugins/store/sqlite/account_auth.go`, `account_auth_test.go`; modify `store.go`, `machine.go`, `sessions.go`, `machine_test.go`, `migration_session_test.go`.
**Consumes:** Shared types above, existing `sessions` table and `newTestStore(t, 0)` fixture.
**Produces:** All methods of `ports.AccountAuthStore`, an explicit schema migration to version 10 (or next available version if the checkout advances), and `BackupBeforeLocalAuth(ctx context.Context, path string) error` on Store for Task 3.

- [ ] Write the generation regression:

```go
func TestPasswordResetRejectsPreviouslyVerifiedGeneration(t *testing.T) {
    db := newTestStore(t, 0)
    ctx := context.Background()
    if err := db.RegisterAccountAuth(ctx, "partner"); err != nil { t.Fatal(err) }
    old, err := db.AccountAuth(ctx, "partner")
    if err != nil { t.Fatal(err) }
    if err := db.SetAccountPassword(ctx, "partner", "encoded-new", old.Generation); err != nil { t.Fatal(err) }
    err = db.CreateAuthenticatedSession(ctx, "stale-session", "partner", old.Generation, time.Now().Add(time.Hour))
    if !errors.Is(err, ports.ErrAuthDenied) { t.Fatalf("stale login: %v", err) }
}
```

Also add named tests `TestLinkRedeemConcurrentSingleWinner`, `TestLinkInsertFailureRollsBackConsumption`, `TestRetiredIDCannotRegister`, `TestResetDeletesOnlyTargetSessionsAndLinks`, `TestLocalAuthMigrationRunsOnce`. Use barrier channels, not sleeps. For redemption rollback, pre-insert the intended session hash to force a uniqueness error, then redeem with a different hash; it must succeed.

- [ ] Run `go test ./plugins/store/sqlite -run 'Test(PasswordReset|Link|RetiredID|ResetDeletes|LocalAuthMigration)' -count=1`; expect missing API/schema failures.
- [ ] Implement the neutral interface and SQL:

```sql
CREATE TABLE account_auth (
  account_id TEXT PRIMARY KEY COLLATE NOCASE,
  password_hash TEXT NOT NULL DEFAULT '',
  generation INTEGER NOT NULL DEFAULT 1 CHECK(generation > 0),
  retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN (0,1))
);
CREATE TABLE web_login_links (
  hash TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  sender_id TEXT NOT NULL,
  generation INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX idx_web_login_links_account ON web_login_links(account_id);
```

Use Unix milliseconds for new link expiry; retain existing session timestamp encoding. Prune expired link rows on mint and redemption. Keep at most one outstanding link per account: minting a new one deletes its older links in the same transaction. No FK to YAML membership.

Implement conditional session insertion:

```sql
INSERT INTO sessions(hash,account_id,created_at,expires_at)
SELECT ?,account_id,?,? FROM account_auth
WHERE account_id=? AND generation=? AND retired=0;
```

Zero affected rows means `ports.ErrAuthDenied`. Link redemption deletes exactly the row matching hash, account, sender, generation, and `expires_at > now`, checks one affected row, then performs the generation-conditional session insert in the same transaction; any failure rolls back both. Generation checks prevent reset/mint and reset/redeem races. `WebLoginLink` returns metadata only, never a raw token.

Create the schema upgrade and invalidate obsolete sessions/login transactions exactly once inside the versioned migration. Do not recreate obsolete login tables from `sessionSchema` after Task 9 removes them. Check newer schema versions before destructive operations. `BackupBeforeLocalAuth` uses SQLite `VACUUM INTO` to a unique backup path, never a plain copy of a WAL-active database, and refuses overwriting a backup. The CLI calls it before enabling the migration; normal open must refuse a version-9 database needing cutover until the migration command has backed it up. Add a narrow `OpenForLocalAuthMigration(path string) (*Store, error)` that opens the existing DB without applying the new auth migration, and `MigrateLocalAuth(ctx context.Context) error` for this offline path; ordinary `Open` creates the schema directly only for a fresh database.

- [ ] Test reopening version 10 does not invalidate fresh sessions; a version-9 backup contains private traces and `auth_records`; a too-new database remains unchanged. Run `go test ./plugins/store/sqlite` and its focused race tests.
- [ ] Stage this task's exact files; commit `feat(auth): persist credentials and atomic web login links`.

## Task 3: Configuration and restart-safe offline cutover

**Files:** Create `internal/config/local_accounts.go`, `local_accounts_test.go`, `cmd/eggyd/migrate_login.go`, `migrate_login_test.go`; modify `internal/config/accounts.go`, `config.go`, `config_init.go`, `config_mutate.go`, `config_validate.go`, their tests; `cmd/eggyd/main.go`.
**Consumes:** Task 2 backup/migration API; existing locked YAML node editor, `LoadOrCreateConfig`, account data migration.
**Produces:** Shared config functions above, `WebConfig.PasswordAccountID`, explicit CLI `eggyd --home <home> --migrate-local-login --password-account <id>`.

- [ ] Add config tests using the existing `accountConfig`, `accountSecrets`, and `loadText` fixtures:

```go
func TestLocalAccountsNeedNoGoogleLogin(t *testing.T) {
    body := accountConfig()
    begin := strings.Index(body, "  google_login:\n")
    if begin < 0 { t.Fatal("fixture lacks Google login section") }
    end := strings.Index(body[begin:], "    client_secret_env:")
    end += begin
    end += strings.Index(body[end:], "\n") + 1
    body = body[:begin] + "  password_account_id: nigel\n" + body[end:]
    body = strings.ReplaceAll(body, "    google_email: nigel@example.com\n", "")
    body = strings.ReplaceAll(body, "    google_email: Partner@Example.com\n", "")
    env := accountSecrets()
    env["EGGY_UI_USER_EMAIL"] = "owner@example.com"
    env["EGGY_UI_PASSWORD"] = "existing-owner-password"
    if _, _, err := loadText(t, body, env); err != nil { t.Fatal(err) }
}
```

Add tests for dangling binding, partial env credentials, alias collision, duplicate case-insensitive IDs, preserved Telegram enablement, bound/last-account removal refusal, and `WithAccount` blocking concurrent config mutation until its callback finishes. Migration fixture tests remove `google_email`/`web.google_login`, preserve outbound `google` exactly, and preserve unrelated YAML nodes/comments.

- [ ] Run `go test ./internal/config -run 'Test(LocalAccounts|LocalLogin|WithAccount)' -count=1`; expect validation failures or missing symbols.
- [ ] Add the binding and update validation. Remove Google email as an account authentication field at final cutover; recognize it only in migration input. The earlier spec's optional-email allowance is superseded by its instruction to retire obsolete inbound fields. Remove `AccountForEmail` callers in Task 9. `EGGY_UI_USER_EMAIL` remains the first account's operator-configured alias, not an outbound identity. No new environment variable.

Normal config loading must detect old login shape and report the migration command before `KnownFields` rejects it; do not prune the old Google fields automatically before backup/preflight. Legacy single-owner and Google accounts both require this explicit cutover, yielding one account session mechanism. For fresh headless setup, change `EGGY_ACCOUNTS` to comma-separated IDs with optional `:telegram_id`; reject the old email form with migration guidance. With multiple accounts, require `web.password_account_id` in config; do not select the first list entry. Single-owner environment initialization can bind the explicit `EGGY_OWNER_ID`, because that is already a declared identity, not list order.

- [ ] Add the CLI flags in `main.go` before daemon startup and implement these ordered operations:

```text
1. Require the daemon to be stopped; take the existing home/config locks.
2. Parse old YAML as nodes, derive candidate accounts, require the explicitly supplied password account.
3. Validate environment credentials, alias collision, public URL, account IDs, and candidate config.
4. Open the old DB via OpenForLocalAuthMigration and persist a prepared cutover marker
   in existing schema_meta, including the original config digest and backup paths.
5. Atomically write config.yaml.pre-local-login with mode 0600 and no overwrite;
   VACUUM INTO eggy.db.pre-local-login. Verify backups before advancing the marker.
6. Run versioned MigrateLocalAuth; seed account_auth rows for all candidate accounts.
7. Run existing legacy account data migration when needed, preserving selected owner and account IDs.
8. Atomically write the validated config via internal/config; remove retired inbound fields.
9. Close DB; print only completion status and backup locations; exit without starting Eggy.
```

Migration code lives in config and SQLite; `cmd/eggyd` orchestrates these calls, never writes YAML itself. Do not reacquire the same non-reentrant file lock by nesting exported mutation functions: use one `internal/config` entry point for the config staging/write stages, with existing unlocked helpers inside it. Do not hold SQLite transactions while acquiring the config lock.

Use existing `schema_meta` for the cutover phase (`local_login_cutover` values `prepared`, `database_ready`, `complete`) rather than a fourth persistence format. On interruption in `prepared`, verify any existing config backup against the saved digest, run SQLite integrity/version checks on an existing database backup, and create only missing backups. On interruption after DB upgrade, reuse the verified backups, seed only missing rows, and complete the config write; never re-clear sessions merely because the marker exists. Startup refuses `database_ready` until the command completes. If backups already exist without a matching migration marker, fail visibly rather than overwrite them. Fresh homes need no old-home backup or cutover marker.

- [ ] Add CLI tests that inject failure after backup, DB migration, and config replacement; rerunning finishes once and retains provider/grant/private data. Verify no secret value appears on stdout/stderr. Run `go test ./internal/config ./cmd/eggyd ./plugins/store/sqlite`.
- [ ] Commit exact task files as `feat(config): migrate deployments to local account login`.

## Task 4: Password login, normal sessions, and safe mode

**Files:** Create `internal/web/account_auth.go`, `account_auth_test.go`; modify `login.go`, `session.go`, `web.go`, `safemode.go`, relevant web tests; `internal/bootstrap/login.go`, `account_directory.go`, `app.go`, relevant bootstrap tests.
**Consumes:** Tasks 1–3; `ports.AccountAuthStore`, existing `SessionStore`, `issueAccountSession` cookie flags.
**Produces:** `WebUIConfig.Auth ports.AccountAuthStore`, `PasswordAccountID string`; reusable helper `setAccountSessionCookie(w http.ResponseWriter, raw string, expires time.Time)` extracted from the current issuer.

- [ ] Extend `accountWebConfig` to register its two fixture accounts; add this HTTP regression:

```go
func TestLocalPasswordIssuesAccountSession(t *testing.T) {
    now := time.Now().UTC()
    cfg, db, _ := accountWebConfig(t, now)
    cfg.Auth = db
    encoded, err := session.HashPassword("partner-password-long")
    if err != nil { t.Fatal(err) }
    auth, err := db.AccountAuth(context.Background(), "partner")
    if err != nil { t.Fatal(err) }
    if err := db.SetAccountPassword(context.Background(), "partner", encoded, auth.Generation); err != nil { t.Fatal(err) }
    handler := NewWebHandler("", cfg)
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"partner","password":"partner-password-long"}`)))
    if response.Code != http.StatusOK { t.Fatalf("status=%d", response.Code) }
    cookies := response.Result().Cookies()
    if len(cookies) != 1 { t.Fatalf("cookies=%d", len(cookies)) }
    got, err := db.SessionAccount(context.Background(), session.HashToken(cookies[0].Value), now)
    if err != nil || got != "partner" { t.Fatalf("account=%q err=%v", got, err) }
}
```

Add a fake auth store that pauses after `AccountAuth` returns; reset the real store before releasing it. The stale verification must return 401 and create no cookie. Cover environment alias, missing account, dummy verification for unknown users, body limits, CSRF, removed accounts, logout, safe-mode missing DB/invalid identity, and legacy cookie rejection.

- [ ] Run `go test ./internal/web -run 'Test(LocalPassword|PasswordReset|SafeMode)' -count=1`; expect absent password path/fields.
- [ ] Replace account-mode Google routing with the existing password endpoint. Resolve identity server-side, load credential/generation, verify outside locks, then revalidate membership with `config.WithAccount` and conditionally create the session. Algorithm:

```text
throttle -> bounded strict body -> resolve username or environment alias
-> acquire one of two verification slots (otherwise 429)
-> verify local hash or environment credential; unknown user or malformed stored hash verifies dummy hash
-> release slot -> on failure record throttle failure and return generic 401
-> WithAccount(current ID): recheck binding + generation, CreateAuthenticatedSession
-> set cookie only after commit -> reset throttle -> 200
```

Prehash the environment password once into the same verification format in memory so known environment failures and local failures perform comparable work. Add `HashEnvironmentPassword(password string) (string, error)` in `plugins/auth/session/password.go`; it accepts 1–256 valid UTF-8 bytes and delegates to the same unexported `encodePassword(password string) (string, error)` used by `HashPassword`. `HashPassword` for user creation still enforces 12 bytes. Test this wrapper in the same package; do not duplicate the KDF. Do not persist this environment-derived hash. If the live binding differs from the configured binding while the process is running, refuse environment login until restart rather than binding a boot-time secret to a newly selected person.

Session resolution also checks `Auth.AccountAuth` is present and not retired; this blocks direct YAML resurrection after removal. SQLite session creation must not call the old unconditional issuer after generation validation. Extract only cookie formatting; retain a single account session mechanism. Replace `loginKind`'s mode inference with local `password` or `unavailable`; safe mode uses the same configured identity and SQLite store.

- [ ] Run `go test ./internal/web ./internal/bootstrap`; verify failed storage operations issue no cookie and closed streams cannot deliver data after revocation.
- [ ] Commit task files as `feat(web): authenticate local users with account sessions`.

## Task 5: People, credentials, and account lifecycle

**Files:** Modify `internal/web/accounts.go`, `account_routes_test.go`, `accounts_test.go`, `web.go`, `internal/config/accounts.go`, its tests; extend new `internal/web/account_auth.go` and bootstrap account initialization.
**Consumes:** `AccountAuthStore`, `config.WithAccount`, existing config mutation functions, session guard and chat hub.
**Produces:** Wire management endpoints specified above; durable retired-ID refusal and visible pending accounts.

- [ ] Add a lifecycle HTTP test to existing account route tests. Use the existing `signIn` helper and `accountWebConfig` fixture. Define this reusable request helper in that test file:

```go
func authenticatedJSON(h http.Handler, cookie *http.Cookie, csrf, method, path, body string) *httptest.ResponseRecorder {
    r := httptest.NewRequest(method, path, strings.NewReader(body))
    r.Header.Set("Content-Type", "application/json")
    r.Header.Set("X-Eggy-CSRF", csrf)
    r.AddCookie(cookie)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, r)
    return w
}
```

Create `third` with a password and Telegram ID; sign in as `third`; reset its password from `partner`; assert the old cookie and any unused `/web` link fail, while `partner` remains signed in. Then delete `third` and attempt creation with ID `THIRD`; assert refusal and unchanged old trace ownership. Additional tests: missing/wrong CSRF, self-change without current password, self-reset bypass attempt, environment account reset refusal, last/bound/self deletion, duplicate sender ID, no credential echo, SQLite failure after YAML write, and removal failure after YAML commit.

- [ ] Run `go test ./internal/web -run 'TestAccount' -count=1`; expect absent endpoints/readiness fields and old Google prerequisites.
- [ ] Implement creation with explicit recovery semantics:

```text
Validate body/password; compute hash outside locks.
Under existing config write lock, reject existing YAML ID and any account_auth row for that ID.
Write membership through internal/config, preserving the same validation and atomic write.
RegisterAccountAuth; set initial password with generation compare.
If either store step fails, return 503/account_created=true, retain YAML membership,
show password_state=pending, and offer the password-setting retry action.
```

To keep creation check and config write serialized without web writing YAML, add `config.AddAccountChecked(path string, input AccountInput, check func(string) error) error`. It runs the check under the same existing `mutate` lock before appending; the callback may only check SQLite, never mutate config. The existing `AddAccount` becomes a thin wrapper only if it still has non-web callers; bootstrap must use the checked route for runtime user creation. No `internal/config` store import is introduced.

Pending users cannot password-login; a mapped Telegram user can still use `/web` once its auth row is registered. Bootstrap registers missing rows for genuinely new active YAML accounts before exposing routes, and reconciles stored IDs absent from YAML to retired rows. Add `AccountAuthRecords(ctx context.Context) ([]ports.AccountAuth, error)` to the store's administrative contract for this reconciliation; its result stays server-side. Account directory resolution checks both YAML membership and a non-retired auth row, including Telegram, Discord, schedules, and heartbeat admission. Do not initialize a second account directory inside the credential subsystem.

After membership removal, immediately close streams, retire credentials, and revoke links/sessions; return an explicit cleanup error if SQLite fails. Even before cleanup succeeds, YAML membership denial stops every ingress. The retained row blocks reuse; failed cleanup is reconciled at next startup. Account creation never interprets an old row as a pending new account. Retrying a password for an already listed pending account is a different operation than creating an ID.

For `POST .../{id}/password`, select target from the admin route only after authenticating the acting principal; never replace the acting principal. When target is self, verify `current_password`; another trusted person can reset the target without that field. Always deny a local password for the environment-bound account. Revalidate membership/binding with `WithAccount`, call the generation-checked setter, and close target streams after commit. Own change logs the user out; explain that and return them to login. `revoke-sessions` uses `RevokeAccountAuth` and is available for environment-backed accounts as the documented manual invalidation operation.

- [ ] Run focused routes and `go test ./internal/config ./internal/web ./internal/bootstrap`. Inject failure between YAML removal and SQLite cleanup, restart, and prove neither the old credentials nor recreating the ID grants access.
- [ ] Commit exact task files as `feat(accounts): manage local credentials and user lifecycle`.

## Task 6: Verified Telegram `/web` and atomic redemption

**Files:** Create `internal/commands/web.go`, `web_test.go`, `internal/web/login_link.go`, `login_link_test.go`; modify `internal/commands/commands.go`, tests; `internal/kernel/events/events.go`; `plugins/channels/telegram/handler.go`, `telegram_test.go`; `internal/bootstrap/app_events.go`, `app.go`, account integration tests; `internal/web/web.go`.
**Consumes:** Existing dispatcher principal, Task 2 link store and session cookie helper, Task 3 current-config lock.
**Produces:** `commands.WithWebLoginSender(ctx context.Context, senderID string) context.Context`; `commands.Options.WebLoginLink func(context.Context, string) (string, error)` wired only in bootstrap; POST link redemption handler.

- [ ] Test `/web` explicitly requires trusted sender context:

```go
func TestWebCommandRequiresVerifiedSender(t *testing.T) {
    calls := 0
    svc := New(Options{
        PublicBaseURL: "https://eggy.example",
        WebLoginLink: func(context.Context, string) (string, error) {
            calls++
            return "https://eggy.example/auth/link#token=secret", nil
        },
    })
    ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "partner"})
    reply, handled, err := svc.Execute(ctx, "/web")
    if err != nil || !handled || calls != 0 || strings.Contains(reply, "token=") {
        t.Fatalf("unverified mint: handled=%v calls=%d err=%v", handled, calls, err)
    }
    _, _, err = svc.Execute(WithWebLoginSender(ctx, "123"), "/web")
    if err != nil || calls != 1 { t.Fatalf("verified mint: calls=%d err=%v", calls, err) }
}
```

Add real webhook tests for bad webhook secret, unmapped sender, group chat, two distinct mapped senders, sender reassignment after enqueue, duplicate update, Discord `/web`, and schedule text `/web`. Only the mapped private Telegram message can mint a token. Add redemption tests for GET/prefetch nonconsumption, missing/foreign Origin, forged/expired/replayed token, reset generation, unlink, store rollback, and no cookie on failed membership resolution.

- [ ] Run `go test ./internal/commands ./internal/web ./internal/bootstrap ./plugins/channels/telegram -run 'Test(WebCommand|WebLogin|TelegramWeb)' -count=1`; expect missing APIs/metadata.
- [ ] Preserve verified ingress metadata in the event envelope, not prompt text:

```go
// internal/kernel/events.Event: provider-neutral authenticated sender metadata.
SenderID string `json:"sender_id,omitempty"`
```

Only the Telegram webhook fills `SenderID` from its verified numeric sender, after private-chat and allowlist checks. Generic selection callbacks do not get the web-login marker, even if their selected text says `/web`; this is a direct-message command. Bootstrap stamps `WithWebLoginSender` only for `TypeMessage` events with explicit Telegram source/destination and nonempty verified sender metadata. The default-to-Telegram behavior of `destination.FromContext` is not evidence of authentication and must never authorize minting. No provider package import is added to kernel/events or ports.

Change `webCommand()` to `webCommand(ctx context.Context)`. Its injected callback checks the principal, current account, `TelegramEnabled`, and exact current sender mapping under `WithAccount`, loads the generation, and mints/stores the token. Return `/auth/link#token=<raw>` with expiry/forwarding warning. Non-Telegram contexts return the bare panel URL and username/password instructions. Minting is unavailable in safe mode because the Telegram runtime is not running.

At redemption, require JSON and a matching non-null `Origin` against the configured public base URL, plus a custom `X-Eggy-Login: 1` header. This unauthenticated POST cannot use session CSRF; the custom header, same-origin policy, no CORS, and explicit click provide the browser boundary. Do not reuse `sameOrigin`'s allowance for absent Origin for this route. Apply body limits and existing login throttling without logging raw token input.

```text
read link metadata by hash -> WithAccount(link.AccountID)
-> verify Telegram enabled, same sender mapping, non-retired auth generation
-> generate session token -> RedeemWebLoginLink transaction
-> set normal account cookie after commit -> return success
```

The cookie may replace an existing session only after the confirmation click. It need not revoke the previous account's independent session globally. Serve the landing shell with `Cache-Control: no-store` and `Referrer-Policy: no-referrer`; ignore/reject legacy `?token=` links. Avoid open redirects: success goes only to `/`.

The current command path in `internal/kernel/turns/turns.go` delivers handled commands before recording conversation data. Preserve that behavior. Assert the link token is absent from conversations, trace bodies, and application logs after a real webhook command; do not invent a second durable secret registry for ephemeral tokens.

- [ ] Run `go test ./internal/commands ./internal/web ./internal/bootstrap ./plugins/channels/telegram ./plugins/store/sqlite`. Run focused race tests for mint/reset, redemption concurrency, and queued sender reassignment.
- [ ] Commit exact task files as `feat(telegram): issue account-bound single-use web links`.

## Task 7: Login, People, setup, and link confirmation UI

**Files:** Create `website/src/WebLoginLinkPage.tsx`, `website/tests/web-login-link.test.ts`, `website/tests/local-login.test.ts`; modify `AccountsCard.tsx`, `LoginPage.tsx`, `SetupPage.tsx`, `api.ts`, `App.tsx`, existing account/setup tests; `internal/config/setup.go`, `setup_test.go`, `internal/web/setup.go`, `setup_test.go`.
**Consumes:** Task 4 login, Task 5 account management, Task 6 link redemption.
**Produces:** Google-free onboarding and functional browser login for both users.

- [ ] Add Bun tests for fragment extraction, click-only redemption, and clearing secrets. Define the extraction helper in `WebLoginLinkPage.tsx` as follows, and write its test first:

```ts
export function takeWebLoginToken(location: Pick<Location, "hash" | "pathname">,
  history: Pick<History, "replaceState">): string {
  const token = new URLSearchParams(location.hash.slice(1)).get("token") ?? "";
  history.replaceState(null, "", location.pathname);
  return /^[A-Za-z0-9_-]{43}$/.test(token) ? token : "";
}
```

```ts
import { expect, test } from "bun:test";
import { takeWebLoginToken } from "../src/WebLoginLinkPage";

test("takes token out of browser history", () => {
  const urls: unknown[] = [];
  const token = "a".repeat(43);
  expect(takeWebLoginToken({ hash: `#token=${token}`, pathname: "/auth/link" },
    { replaceState: (_data, _unused, url) => { urls.push(url); } })).toBe(token);
  expect(urls).toEqual(["/auth/link"]);
});
```

Component tests, using the existing happy-dom/Bun pattern, assert no fetch on mount/GET, one POST after clicking Continue, a disabled button while pending, no local/session storage use, a generic expired-link message, and no automatic retry after ambiguous network failure. Show “Continue using the account that requested this Telegram link. This may replace your current sign-in.” If a session already exists, show its current username so switching is not silent; do not disclose target account information through a public token-inspection endpoint.

- [ ] Run `bun test tests/web-login-link.test.ts tests/local-login.test.ts tests/accounts.test.ts` from `website/`; expect missing helper/component/controls.
- [ ] Render `/auth/link` before the normal authenticated/unauthenticated App branches. Capture the fragment once on initial entry, strip it immediately, hold it in component memory, and clear it on success/unmount. Refresh after fragment removal intentionally requires requesting a new `/web` link. POST using `{token}` and `X-Eggy-Login: 1`; browsers supply Origin. After success clear cached identity/CSRF, call `checkSession`, then navigate to `/`.

Update the ordinary login form to Username/Password and send `username`. Remove Google buttons and inbound login-client form. People creation asks for immutable username, password, and optional numeric Telegram ID; password inputs use `autocomplete="new-password"`, are cleared after submission, and are never placed in invite copy. Use `autocomplete="username"`/`current-password` for login. Show pending failures with Retry password setup; new users are not marked ready merely because a YAML row exists. Show local-password reset and separate Revoke sessions controls; environment-bound users see “Managed in deployment environment”.

First-run setup continues accepting only non-secret values and environment variable names: it creates the first account bound to the existing environment credentials after checking their presence. It never accepts the environment password through setup, reads it back to the browser, or writes `.env`. Adding subsequent local users happens only in the authenticated People UI. Update `SetupInput` to remove Google email/client fields and bind its explicit `AccountID`. Numeric Telegram ID can be added through People afterward or supplied as an optional setup field validated by the same config rules.

- [ ] Run focused frontend tests, `bun test`, `bun run build`, and `go test ./internal/config ./internal/web`. Inspect the new screens in a browser if available, including narrow mobile layout. Report browser unavailability without claiming visual verification.
- [ ] Commit task files as `feat(ui): onboard local users and redeem Telegram web links`.

## Task 8: Personal user configuration with shared integrations

**Files:** Modify `website/src/ConfigPage.tsx`, `AccountsCard.tsx`, `api.ts`; create `website/src/PersonalSettingsCard.tsx`, `website/tests/personal-settings.test.ts`; extend `internal/web/agent.go`, `agent_test.go`, `internal/kernel/services/agent_runtime_test.go`, `internal/bootstrap/accounts_integration_test.go` only where existing surfaces lack a preference read/write. Extend `internal/web/approvals_test.go`, watch/schedule tests as needed. No generic settings table or user YAML files.
**Consumes:** Existing `AgentRuntime`, `StateStore`, approval mode service, context store, schedule store, new credential/session routes.
**Produces:** Explicit “My settings”, “People”, and “Shared deployment” areas backed by existing authorities.

The user confirmed this scope during planning: personal login, Telegram link, model/approval preferences, memory, and schedules; shared provider keys and tool connections. Apply this ownership matrix:

| Setting/data | Authority | Who edits / effect |
|---|---|---|
| Immutable account ID, Telegram/Discord bindings | `config.yaml accounts` through `internal/config` | Trusted People management; link ownership checks remain |
| First user's environment login | `.env`/operator environment plus YAML account binding | Operator changes; restart required |
| Other local passwords, link/session revocation | SQLite `account_auth`, `web_login_links`, `sessions` | Self-change or explicit trusted administrative reset |
| Selected model, reasoning effort, thinking visibility | Existing `machine_state.agent` by account | Calling user only, effective immediately across Telegram/web |
| Approval mode | Existing `machine_state.approval_mode` by account | Calling user only; never copy another user's `auto` |
| USER.md, MEMORY.md, WATCH.md | Existing account-owned Markdown context | Calling user's documents, existing size/secret rules |
| Schedules and usage counters | Existing account-scoped SQLite records | Calling user only; no shared duplicate |
| Conversations, traces, approvals | Existing account-scoped SQLite records | Calling user only |
| Provider API keys/model catalog, tool/MCP/Google connections | Existing deployment config/environment/sealed grants | Shared; all trusted users can administer |
| Heartbeat cadence, global timezone, trace retention, appearance | Existing deployment config | Shared in this change; label explicitly |

Do not move personal preferences into `accounts[]`, copy an entire config per user, create `user_config.json`, or store a generic preferences blob parallel to `machine_state.agent`. A new user's runtime settings start from deployment defaults, not the creating user's private preferences. Personal model reset writes the existing empty alias and follows the configured default. Removed aliases retain the existing fallback behavior. Approval defaults retain `normal` unless deployment config explicitly specifies otherwise; neither onboarding nor migration may enable `auto` on anyone's behalf.

- [ ] Add the model isolation test below and corresponding HTTP tests that try to submit another `account_id` while signed in as the first user:

```go
func TestPersonalModelSelectionDoesNotChangeAnotherAccount(t *testing.T) {
    store := newStateStore(t)
    runtime := NewAgentRuntime(store, "shared-default", []string{"shared-default", "other"}, nil)
    a := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "a"})
    b := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "b"})
    if err := runtime.SelectModel(a, "other"); err != nil { t.Fatal(err) }
    if got, err := runtime.SelectedModel(b); err != nil || got != "shared-default" {
        t.Fatalf("second user's model=%q err=%v", got, err)
    }
}
```

Add concrete regressions: A chooses `auto` and B stays `normal`; A hides thinking and B still sees it; A's watch edit/schedule create is absent for B; A's settings survive password reset and a new `/web` session; a newly created C inherits no A/B preferences; provider key/config is unchanged when any personal control is used. Tests for already correct backend behavior should pass; the new UI test should fail until the scoped settings area exists. Do not modify correct store implementations just to make this task larger.

- [ ] Run `go test ./internal/kernel/services ./internal/web -run 'Test(Personal|AgentRuntime|ApprovalMode)' -count=1` and `bun test tests/personal-settings.test.ts` from `website/`.
- [ ] Build `PersonalSettingsCard` using existing `GET /api/agent`, `POST /api/agent/model`, `/api/agent/effort`, and `GET/POST /api/approvals/mode`. Reuse existing Watch and Schedules cards under My settings. Surface own credentials and channel linkage through the existing People row actions instead of implementing a second identity editor.

If thinking visibility lacks a structured endpoint, minimally extend `AgentSwitch` with existing runtime methods `ShowThinking(context.Context) (bool, error)` and `SetShowThinking(context.Context, bool) error`; add `show_thinking` to `/api/agent` and `POST /api/agent/thinking` with `{ "show": true }`. Implement the route exactly like the existing model/effort handlers, calling the existing service; do not create a new state field. Update test fakes to satisfy the interface. The runtime service remains the only writer shared with commands.

UI headings and copy: “My settings — applies to your Telegram and web sessions”; “People — trusted users who can administer this deployment”; “Shared deployment — changes here affect everyone”. Users can navigate to all three because there are no roles. Shared resource controls must not look like personal preferences. Keep existing visual style and avoid editing ChatPage.

- [ ] Run service/web/SQLite isolation tests plus `bun test` and `bun run build`. Verify setting changes from Telegram appear in the web panel and vice versa for the same account, without affecting the other account.
- [ ] Commit task files as `feat(settings): expose personal configuration with shared integrations`.

## Task 9: Remove obsolete inbound login and verify the complete cutover

**Files:** Remove `internal/web/google_login.go`, `google_login_test.go`, `plugins/auth/google/oidc.go`, `oidc_test.go`, `plugins/store/sqlite/identities.go`, its tests when no callers remain. Modify `internal/bootstrap/login.go`, account fixtures/integration tests, `plugins/store/sqlite/sessions.go`, `internal/web/session.go`, `internal/config/accounts.go`, `config.go`, `.env.example`, `config.example.yaml`, `AGENTS.md`, `go.mod`, `go.sum`; existing docs below. Remove obsolete signed-cookie helpers/tests only after their setup/recovery callers are checked.
**Consumes:** All prior tasks.
**Produces:** One deployed login/session model, accurate documentation, and measured validation evidence.

- [ ] Replace Google-login setup in bootstrap integration fixtures with local credential/session setup while preserving tests for shared outbound Workspace grant identity/generation. Keep negative ownership tests; deleting a provider fixture is not permission to delete the isolation test it happened to serve.

Add a full two-user integration test named `TestLocalAccountsPasswordAndTelegramWebRemainIsolated`. Through real HTTP/webhook handlers: log in A by environment password; create B with a local password and Telegram ID; log B in by password and `/web`; create distinct conversations/traces and preferences; prove A cannot fetch B's private trace ID; prove B can manage People; remove B and assert its password, existing session, queued event, unused link, and open stream cannot access data. Use the existing fake shared model/provider and verify both users reached that same configured provider. Do not expose an API key through test output.

- [ ] Run `go test ./internal/bootstrap -run 'TestLocalAccountsPasswordAndTelegramWebRemainIsolated' -count=1`; finish any integration gaps rather than weakening assertions.
- [ ] Delete retired inbound routes, identity-binding/reset UI, OIDC verifier, Google Web client secret field, old password/email request alias, account-mode branches whose only purpose was selecting a login mechanism, and obsolete SQLite login-transaction methods. Versioned cutover drops old `identities` and `login_transactions`; keep backup artifacts and retain outbound `auth_records`, grant sealers, `plugins/tools/google/oauth.go`, and `plugins/tools/mcp/oauth.go`.

Use `rg` to check all remaining references:

```sh
rg -n 'GoogleLogin|GoogleLoginClientSecret|AccountForEmail|google_login|SignSession|VerifySession|SignLoginLink|VerifyLoginLink' internal plugins website/src cmd
rg -n 'EGGY_GOOGLE_LOGIN|sign in with Google|Google Sign-In' .env.example config.example.yaml docs/src/content AGENTS.md
```

Remaining old names are allowed only in migration readers/tests and historical planning documents. Do not remove a shared cookie utility used by setup; remove only the dead legacy authentication functions. Run `go mod tidy` and inspect the dependency diff; do not remove OAuth/MCP transitive dependencies still in use or make unrelated version upgrades.

- [ ] Update these docs with exact user flows and ownership scope:
  - `docs/src/content/docs/get-started/quickstart.md`: first environment login, People creation, personal settings.
  - `docs/src/content/docs/get-started/deploy-railway.md`: migration command with stopped daemon and mounted home, backups and restart.
  - `docs/src/content/docs/use/telegram.md`: `/web` expiry, explicit confirmation click, account mapping.
  - `docs/src/content/docs/operate/security.md`: local password storage, shared integrations, user ownership, password reset versus environment rotation.
  - `docs/src/content/docs/operate/safe-mode.md`: local account recovery and host-repair failure cases.
  - `docs/src/content/docs/operate/persistence-memory.md`: credential/link records and per-account settings authority.
  - `AGENTS.md`: replace inbound Google-only identity guidance; retain dedicated outbound Workspace identity and equal-account capability rules.

Document rollback precisely: stop new binary, restore both paired pre-cutover config and database backups, restore compatible old binary, then start. Do not run the old binary against the new schema. Updates made after cutover are not in the backup; a later rollback is an operator decision, not automatic recovery. No actual deployment is part of implementation unless separately requested.

- [ ] Run required verification from repo root:

```sh
make fmt vet test race build
```

Run frontend `bun test` and `bun run build` from `website/` if not already covered by the Make targets. Run `make smoke` when Docker is available; otherwise record the actual daemon blocker. Run `git diff --check`; inspect `git status --short` and `git diff --cached --name-only` before committing. Do not stage unrelated ChatPage/reply-selection changes.

- [ ] Record actual production added/deleted lines and net config/tool/table/loop changes. Expected: one new binding config key; removed Google-login fields; two new auth tables replacing two inbound-login tables; zero new tools/loops/provider keys. Existing `sessions` and `machine_state` stay authoritative. The `account_auth` lifecycle row also retains retired-ID markers; this does not add a third new durable record type. Commit only task-owned files as `refactor(auth): retire Google sign-in and complete local account cutover`.

## Coverage and handoff

| Requirement | Tasks |
|---|---|
| Environment login retained for first account | 3, 4, 7 |
| Local username/password users | 1, 2, 4, 5, 7 |
| Trusted users can manage People | 5, 7, 9 |
| Five-minute, one-use `/web` links for verified sender | 2, 6, 7 |
| Distinct private traces/data with shared OpenRouter/tools | 8, 9 |
| Personal settings and shared-integration ownership | 8 |
| Reset/revocation and no deleted-ID reuse | 2, 4, 5, 9 |
| Breaking but data-preserving migration and rollback | 2, 3, 9 |
| Safe mode without unsafe fallback | 4 |
| No inbound Google requirement; outbound connection preserved | 3, 7, 9 |

Planning verification: repository contracts inspected; referenced primary hashing documentation checked; plan self-reviewed for ownership, migration ordering, and specification coverage. No implementation tests are claimed as passing. Before implementation, review this plan and select native or subagent-driven execution; native is recommended because the interfaces and cutover sequence are tightly coupled.
