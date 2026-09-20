# Username/password accounts and Telegram web access

Status: user-confirmed direction: local username/password accounts and short-lived, single-use Telegram `/web` login links. Breaking config and schema migrations are permitted for this hobby project. Planning only; no implementation or deployment.

## Intended outcome

Two trusted people share one Eggy deployment and the same OpenRouter configuration, tools, and outbound Workspace connection. Each has their own username/password, Telegram sender ID, conversations, traces, memory, schedules, approvals, and preferences. All trusted accounts can manage People; the first account is not a separate administrator role.

The first account keeps its existing environment login. New accounts receive local username/password credentials. From a linked private Telegram chat, `/web` gives a short-lived, single-use link that signs the sender into their own web account. Google Sign-In is not part of this onboarding flow and requires no setup. Backward compatibility with Google-only login is not a requirement. Migrate this deployment to the selected local-login flow and remove obsolete inbound Google Sign-In wiring where it no longer serves that flow; keep outbound Workspace authorization separate.

## Current evidence

- `internal/config/accounts.go` requires Google emails and a Web login client in account mode; `config_validate.go` rejects the existing environment panel credentials in that mode.
- `internal/web/login.go` currently issues legacy signed cookies. `internal/web/session.go` already provides opaque, revocable SQLite account sessions and CSRF protection; both new login paths must use these.
- `internal/commands/commands.go` currently returns a Google-login instruction for `/web` in account mode. Its legacy link has no account identity and is not suitable for multiple users.
- `internal/bootstrap/account_directory.go` reloads and validates account configuration. Account removal must continue to fail closed at ingress and on session resolution.
- The checked-out Google adapter uses a dedicated Workspace user's OAuth grant; the reported live service-account change has not been inspected. This design changes neither that adapter nor its credentials.
- `plugins/store/sqlite/machine.go` currently declares `MachineStateVersion = 9`.

## Selected design

Reuse the existing account directory, password login endpoint, session issuer, People card, and `/web` command. Extend authentication to local credentials and account-bound Telegram links. Do not add a role system, separate agent runtime, or per-user provider key.

A shared password for both people was rejected because it does not identify whose private records to use. Google-based onboarding is deferred by user instruction.

### Accounts and local credentials

Use the existing stable account ID as the new user's username, avoiding a second mutable identifier. Existing environment login names remain accepted for the explicitly bound first account.

Add `web.password_account_id` to bind `EGGY_UI_USER_EMAIL` and `EGGY_UI_PASSWORD` to exactly one configured account. Never infer this binding from account order or accept an account selector from a login request. The environment login name must not collide with another account's username. Operators change this password in the environment and restart; the panel does not edit `.env`.

Store new accounts' salted, versioned password hashes in SQLite, keyed by account ID. Never store raw passwords in YAML, logs, traces, or durable conversation context. Use a maintained password-hashing implementation, with parameters and input limits selected and tested in the executable plan; ordinary SHA-256 token hashing is not password hashing. No local password record is allowed for the environment-bound account, avoiding two password authorities for one account.

Keep account membership and Telegram IDs in YAML under the existing config mutation lock. Keep machine-managed credentials and web links in SQLite. Google email is not needed for local identity. Remove obsolete inbound email fields during the authorized migration, preserving outbound Workspace identity settings. The target local-login configuration creates no Google login adapter, sealer, or routes. Retire obsolete inbound Google-login config through an explicit migration rather than keeping a parallel compatibility flow.

### Creating, resetting, and removing users

The People form creates the account through `internal/config`, then sets its password through the credential store. These stores cannot commit atomically: an account whose credential write fails remains visibly pending, with a retryable password-setting action. Never report successful password setup before SQLite commits. A configured Telegram mapping may independently allow `/web`, even while a password is pending; show these capabilities separately.

All authenticated trusted accounts can add users, set/reset another local user's password, and remove users under the existing administrative trust model. Resetting a password revokes that account's existing sessions and unused web links in the same SQLite transaction as the credential update. Require current password for an ordinary self-service password change; a trusted person's administrative reset is explicitly labeled as a reset. Passwords are provided directly to the recipient outside Eggy's automated messaging; do not send credentials automatically.

Remove membership first through the config authority, then invalidate credentials, sessions, links, and open streams. Failure in cleanup must not restore authorization: every authentication and session resolution checks live membership. Before reusing a removed account ID, explicitly clear old credentials and links; do not silently hand a new person the old account's private history. Preserve current historical ownership and adopt an explicit ID-reuse refusal if no existing mechanism prevents this.

Protect the last account and the environment-bound account from accidental deletion. Moving/removing the environment binding must be an explicit validated configuration operation. No per-account credentials or secrets appear in account-list responses.

### Password login and sessions

Extend the existing password handler and throttle. The existing login request field may remain `email` for wire compatibility while the UI labels it Username and accepts account IDs. Resolve the environment alias or local username on the server, verify credentials, check current membership, and call `issueAccountSession`. Both successful methods produce the same opaque account cookie and use the same session guard, CSRF, logout, and stream revalidation.

Unknown users and wrong passwords return the same generic failure. Bound password input sizes, retain throttling before expensive hashing, and avoid username enumeration through markedly different verification paths. Disabled/unconfigured login methods fail closed.

Environment-password changes take effect after restart and prevent new logins with the old password. Existing sessions are not implicitly revoked by this minimal environment path; document the existing explicit session-revocation operation for that case. Local password resets do revoke sessions as described above.

### Telegram `/web`

Pass the command's verified principal into account-mode `/web`; the current helper has no context and must change. Mint links only for an authenticated, mapped private Telegram sender. A user-supplied account ID, message text, group message, scheduler turn, or another ingress cannot choose or manufacture the identity. Keep Telegram webhook authentication, sender allowlisting, and update deduplication intact. Do not automatically enable this feature for Discord.

Generate an opaque cryptographically random token using `plugins/auth/session.NewToken`. Store only its hash with account ID, the issuing Telegram ID, and a five-minute expiry in SQLite. The returned link uses the configured public base URL. Redemption verifies that the account still exists and that the same Telegram ID remains linked, so removing or reassigning the sender invalidates outstanding links.

Use a landing page followed by an explicit same-origin POST to redeem the link; GET requests and Telegram previews must not consume it. Place the token in the URL fragment, remove it from browser history immediately after reading it, retain it only in page memory until redemption, and send it only in the redemption POST. Do not load third-party resources on that page or include the token in logs, analytics, or error reports. Deliver the command reply directly without retaining its raw bearer token in durable conversation records.

Redeem the token and create the normal account session in one SQLite transaction. Concurrent redemption has exactly one winner; an expired, used, removed-account, or unlinked-sender token is rejected. Do not use the legacy in-memory spent-link map or an account-less signed token. Delete expired link rows opportunistically during existing mint/redeem operations; no cleanup goroutine is needed.

`/web` is a bearer credential: whoever redeems it first receives that account's access. The Telegram reply states that it expires in five minutes and should not be forwarded. The resulting session has the same lifetime and privileges as password login.

### Conversion, setup, and safe mode

Conversion explicitly selects the historical owner, binds the existing environment login to it, and preserves the Telegram mapping and enablement. Do not clear the entire Telegram section as the current converter does. Keep the existing data migration and its ownership checks.

First-run setup and People no longer ask for a Google email or Web client for local onboarding. People shows password readiness and Telegram linkage independently. Invite copy explains username/password login and `/web`; it never requires Google enrollment. Keep existing self-service Telegram pairing and its ownership checks alongside trusted manual numeric-ID configuration.

Safe mode uses the same account identity and session database for local password authentication when identity configuration remains valid. It must not fall back to the legacy account-less cookie when identity parsing, account binding, or database access fails. In that case, require host repair. Recovery follows the migrated local-login configuration; a Google-only recovery compatibility path is not required.

### Shared resources and private records

The first account and every added account have equal Eggy capabilities, including People management. This administrative trust does not change private-store ownership checks or allow a submitted account ID to select the acting principal. Each user views their own traces after either password login or `/web` redemption. The OpenRouter key and outbound Workspace grant remain shared deployment resources, not per-user credentials.

Changing the Eggy panel password does not change a Workspace credential. Do not migrate outbound Google authentication in this work.

## Personal user configuration (confirmed during implementation planning)

The user confirmed that personal configuration includes login, Telegram linkage, model/approval preferences, memory, and schedules, while provider keys and tool connections remain shared. This is part of the current scope, not a later settings subsystem.

Use the existing account-scoped `machine_state.agent` for selected model, reasoning effort, thinking visibility, and usage; `machine_state.approval_mode` for approval mode; existing account Markdown for USER.md/MEMORY.md/WATCH.md; and existing account-scoped schedule rows. Do not create per-user YAML config files, a generic preferences table, or duplicate service handlers. New users inherit deployment defaults rather than the creator's preferences. Never copy another user's `auto` mode.

The panel separates My settings, People, and Shared deployment. All trusted users retain access to People and deployment administration; this separation communicates which changes affect whom, not roles. Private settings use only the authenticated principal, never a supplied account selector. Heartbeat cadence, global timezone, trace retention, and appearance remain shared settings and must say so. Per-user integrations and new timezone/theme override systems are outside this change.

Implementation refinement: remove obsolete inbound `google_email` fields during the authorized migration, rather than retaining unused optional authentication metadata. Preserve outbound `google.expected_email` and its identity enforcement. The implementation plan is `docs/superpowers/plans/2026-09-20-local-accounts-and-telegram-web.md`.

## Delivery sequence and acceptance tests

Each task starts with a failing focused test, implements the smallest change, and reruns that test before broader verification. This sequence is a design-level plan; the executable plan must pin credential hashing parameters, exact store interfaces, SQL migrations, and API request shapes before implementation.

1. **Config and account identity.** Modify `internal/config/config.go`, `accounts.go`, `config_validate.go`, and existing tests. Cover explicit password binding, optional Google email, empty-email lookup, username/alias collisions, absent Google config, migration from legacy and Google-only configuration, removal protection, and conversion preserving historical ownership and Telegram enablement. Run `go test ./internal/config`.
2. **Credential and link storage.** Add focused account-auth storage beside `plugins/store/sqlite/sessions.go`; extend `machine.go` and migration tests. Add password hashing helpers under `plugins/auth/session`. Test versioned password verification, invalid hashes, atomic reset/revocation, atomic link redemption/session creation, expiry, concurrent single-use redemption, database failures, and existing-home migration. Raise `MachineStateVersion` from the current 9 to the next available version; never overwrite an intervening migration. Run `go test ./plugins/auth/session ./plugins/store/sqlite`.
3. **Password authentication and recovery.** Update `internal/web/login.go`, `session.go`, `web.go`, `safemode.go`, `internal/bootstrap/app.go`, and `login.go`. Reuse account sessions. Test both local and environment logins, wrong credentials, throttling, current membership, CSRF, logout, stream invalidation, no legacy-cookie fallback, absence of retired Google login routes, and safe-mode failure cases. Run `go test ./internal/web ./internal/bootstrap`.
4. **Account-bound `/web`.** Update `internal/commands/commands.go`, its tests, Telegram/bootstrap wiring, web redemption routes, and the frontend landing flow. Test two verified Telegram senders obtaining distinct sessions, untrusted ingress refusal, missing principals, unlink/reassignment before redemption, previews leaving tokens intact, one redemption winner, restart persistence, and no durable raw-token logging. Run `go test ./internal/commands ./internal/web ./internal/bootstrap ./plugins/store/sqlite` plus focused frontend tests.
5. **People and setup.** Update `internal/web/accounts.go`, `internal/config/setup.go`, `internal/web/setup.go`, `website/src/AccountsCard.tsx`, `LoginPage.tsx`, `SetupPage.tsx`, `api.ts`, and `App.tsx` as needed. Test username/password creation and reset, partial cross-store failures, account removal and ID reuse, independent Telegram readiness, accurate invite text, and no Google requirement. Run focused Go tests, then `bun test` and `bun run build` from `website/`.
6. **End-to-end isolation and docs.** Extend `internal/bootstrap/accounts_integration_test.go` and existing SQLite isolation/migration tests. Exercise both users through password and Telegram `/web` sessions against the shared provider; prove distinct traces, conversations, approvals, and preferences, cross-account access denial, and revocation on subsequent ingress. Update `AGENTS.md`'s Google-only inbound identity description, `.env.example`, and relevant onboarding docs. Preserve the separate outbound Workspace invariant.

Final implementation verification: `make fmt vet test race build`; `make smoke` when Docker is available. Report unavailable Docker as a smoke-test blocker, not a pass. No implementation tests were run for this documentation change.

## Migration policy

The user explicitly permits breaking migrations for this hobby project. Prefer one clear resulting account/login model over long-lived compatibility branches. Preserve existing private records, account IDs where possible, Telegram mappings, provider configuration, and the outbound Workspace grant; permission to migrate does not mean permission to discard them.

Before applying a migration, back up the affected config and database. Validate the intended environment-login binding and ensure a usable local login exists before retiring Google-only login. Existing additional accounts can regain panel access through their mapped Telegram `/web` link or an explicitly set local password; do not invent or expose default passwords. Invalidate obsolete sessions and login transactions at cutover. Make migration restart-safe, with an explicit version and clear failure reporting, and retain the pre-migration backup for rollback. Update account-owned Markdown paths only if account IDs actually change.

No compatibility branch is required merely to support the pre-migration config or login scheme. Remove obsolete inbound Google-login fields, code, tests, and documentation as part of the final implementation scope once their callers are gone. Do not remove shared grant storage or outbound Google OAuth code that still has a live caller.

## Footprint budget

- Production lines: replace mandatory Google onboarding assumptions; reuse the password endpoint, account session issuer, token generator, config authority, and existing command. New code is limited to password credential management and account-bound link redemption; report actual added/deleted production lines in implementation review.
- Config keys: one, `web.password_account_id`; no new environment secret variable or per-user provider keys.
- Durable record types: two new SQLite record types, local password credentials and short-lived login links. Existing account sessions are reused. No raw passwords or tokens are durable.
- Tools: zero. Background loops: zero. Frameworks, databases, processes, replicas, and roles: zero new.
- Explicit SQLite migration and machine-state version bump are required.
- Unrelated edits in `website/src/ChatPage.tsx` and `website/tests/reply-selection.test.ts` remain outside scope.

## Review boundary

This revision records the confirmed local-login and Telegram `/web` direction and permission for breaking migrations. It does not implement it or change credentials, runtime config, Google connectivity, commits, or deployments.
