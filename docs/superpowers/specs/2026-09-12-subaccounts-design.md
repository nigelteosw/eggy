# Two Eggy Users and a Shared Google Workspace Identity

Status: design for implementation planning; implementation has not started.

## Goal and agreed scope

Run one Eggy deployment for two designated people. Each person signs in as themselves and has private conversations and memory. Eggy connects to Google as its own dedicated Workspace user, with its own mailbox. People explicitly share calendars and Drive resources with that identity, or send/forward email to its inbox.

The Google identity is a Workspace user, not a Google Cloud service account. Retain the existing outbound Google OAuth implementation. Do not add domain-wide delegation, user impersonation, personal Google API connections, or a second Google tool implementation.

## Design defaults

These are concrete planning defaults, not additional requirements supplied by the user:

- One administrator and one member, configured in YAML; no public registration or invitation system.
- Private conversations, memory, watch lists, schedules, traces, approvals, and model/approval-mode preferences.
- Both users can operate the same Google connection. Resources accessible through that connection are shared, including Eggy's inbox. Private chat does not make a Google operation or its result private from the other user.
- Configuration, restart, Google connection management, MCP, repository access, shared skills editing, and raw operational diagnostics are administrator-only in this version.
- Shared SOUL.md and reviewed skills remain administrator-managed. USER.md, MEMORY.md, and WATCH.md live under account-specific directories.
- Telegram remains optional, with a distinct numeric Telegram identity mapped to each account. No username-based identity matching.
- The application does not expose another user's private history to the administrator. The deployment operator still controls the database and filesystem; this is application isolation, not isolation from the host operator.

## Authentication and authorization

Google Sign-In is a new inbound OIDC adapter under `plugins/auth/google`. It is separate from `plugins/tools/google`, which continues to own outbound authorization. Use a separate Web application OAuth client for sign-in; retain the current Desktop OAuth client and exact loopback redirect for the shared Google tools connection.

Use authorization code plus PKCE, random state, and nonce. Verify signature, issuer, audience, expiry, nonce, and verified email before resolving an account. Request only `openid email profile`; never retain login access or refresh tokens. Use a maintained OIDC verifier rather than hand-writing JWT/JWKS verification. Its provider-specific details stay in the adapter.

An unbound account can enroll only with its configured Google email. Store the verified issuer/subject binding in SQLite with unique constraints. Subsequent logins require the binding, not just an email match. Email configuration changes do not silently rebind an existing identity. Re-enrollment requires an administrator action through the single config authority plus explicit session/binding invalidation. Do not collapse dots or plus-addresses when comparing enrollment addresses.

The browser receives a random opaque session token. Store only its hash in SQLite, with account ID, creation time, and expiry. Sessions expire after 12 hours. Logout revokes the row; account removal or identity reset revokes all of that account's sessions. Cookies are Secure, HttpOnly, Path=/, and SameSite=Lax; protect all mutating browser routes with CSRF validation and same-origin checks. The OIDC callback uses its single-use state rather than normal authenticated-route CSRF handling.

Store short-lived login transactions in SQLite with a browser-binding cookie hash, state hash, nonce, PKCE verifier, and expiry; atomically consume them once. Seal the verifier with the existing encryption-key mechanism. Expire after five minutes and prune during authentication operations, without a cleanup loop. Reject callbacks on a different browser, replay, missing state, denial, and expired transactions. Redirect only to a fixed local page.

Replace expiry-only sessions and password login in the account-enabled configuration. Remove the bearer `/web` login-link bypass; `/web` returns the panel URL requiring Google Sign-In. Legacy single-owner config may continue to use its existing authentication until explicitly migrated, but must use the same account-aware runtime after normalization. Never allow legacy password/link authentication into the new two-account deployment.

Resolve identity at trusted ingress, then carry an immutable provider-neutral account principal through requests and turns. Browser JSON, URL account IDs, model tool arguments, and Telegram text cannot choose the acting account. Recheck enablement and authorization at queued execution and approval execution, not only at login.

Authorization is independent of approval mode. `auto` never bypasses account or role checks. Members can approve only their own authorized operations. An approval binds account, action, payload, and shared integration generation so reconnecting Google cannot cause an old approval to execute against a different identity.

## Data and runtime boundaries

Preserve one `eggyd` process, one replica, one event loop, one scheduler, and one SQLite database. Use a provider-neutral principal in context to scope existing context-taking store interfaces; missing principals fail closed on private reads and writes. Do not use mutable current-user globals or construct an App/event loop per account.

Add account ownership to messages, threads, resets, traces, approvals, schedules, and proactive accounting. Scope joins and FTS recall as well as primary lookups. Trace spans inherit ownership through a checked parent trace. Split singleton runtime state into account rows for approval mode, model preferences, and repository session state; keep genuinely global configuration and shared grants global. Preserve deduplication semantics with source-qualified event identifiers.

Extend the existing event owner field to carry the stable Eggy account ID instead of adding a competing actor field. Dispatcher validates it against configured accounts and injects the principal. Scheduled work reconstructs identity from the owning schedule, never from instruction text. Keep the current unprompted-turn restrictions: no MCP and no mutations.

Key active turns, steering, cancellation, destinations, and web streams by account plus conversation/thread. Recheck thread ownership before registering SSE. Close existing streams on revocation and check authorization before further delivery. Route Telegram replies and approvals to the account's configured private chat; never fall back to the first configured user.

## Shared Google connection

Keep one sealed grant at the existing `google/workspace` key. Add an expected Workspace email configuration and verified identity metadata to the grant. Verify the outbound grant's identity using Google's identity endpoint with identity scopes, and reject a mismatch before replacing the existing grant. Preserve forced consent, PKCE, refresh-token handling, granted scopes, pending window, fixed endpoints, exact loopback redirect, and per-record associated data. Do not import the inbound login adapter into the outbound Google adapter.

An existing grant with no identity metadata must be verified before it is usable by the member. A failed verification disables tool execution and asks the administrator to reconnect; it must not delete the old grant. Increment the connection generation on replacement/disconnect and invalidate outstanding approvals for the previous generation.

Display the verified Google email and “Shared with both Eggy users” beside Google connection status and approval summaries. Member responses never expose tokens, raw config, or OAuth authorization-management controls.

## Migration and recovery

Normalize an existing owner into an internal administrator account. On explicit conversion to `accounts`, require `migration_owner_id` to name the account receiving historical data; do not infer from list order. Persist the completed import mapping and reject conflicting retries. Back up a stopped deployment's complete home before migration.

Bump `sqlite.MachineStateVersion` from 6 to 7 if 6 is still current at execution. Upgrade database ownership transactionally, preserving primary IDs and assigning all legacy private records to the selected administrator. Preserve shared Google/MCP grant keys and ciphertext. Invalidate pending legacy approvals because they lack account/integration binding. Legacy session tokens are rejected by account-mode handlers.

Move Markdown with an idempotent staged copy/verify/archive sequence, recording progress in SQLite; filesystem operations cannot be included in the SQL transaction. Preserve original files until verified copies exist, never overwrite a nonidentical destination, and do not serve traffic until both phases finish. The second account starts with empty personal documents/history. Old binaries must refuse the upgraded database. Rollback restores the complete pre-upgrade backup and old configuration, not just an older binary.

In account mode, safe mode must not fall back to the global password. It may expose admin recovery routes only if validated identity configuration and the session database are available. If configuration cannot establish administrator identity, serve only generic unauthenticated health information and require operator repair of YAML on the host. Keep restart's existing preflight and in-flight draining behavior.

## Footprint and constraints

- Go 1.26; standard library HTTP and existing ports/adapters architecture.
- One process, one replica, one event loop, one scheduler, one SQLite database.
- YAML startup configuration, Markdown owner documents, SQLite machine records only.
- Zero new model tools and zero new background loops.
- New configuration: accounts with ID/role/email/optional Telegram ID; inbound Google login client settings; expected shared Google email; explicit legacy-owner migration mapping.
- New durable categories: identity bindings, sessions, and transient login transactions; ownership columns reuse existing record categories.
- Production code grows for authentication and isolation. Delete superseded account-mode singleton plumbing and bearer login links; do not set an arbitrary line-count ceiling or add a general RBAC framework.
- Register through bootstrap; config mutation stays under `internal/config` and its existing lock/validation.
- Preserve approval mechanism, secret filtering, atomic Markdown writes, webhook authentication, allowlisting, and update deduplication.

## Acceptance

Two allowlisted users can sign in and use the shared Google connection. Neither can read, mutate, approve, stream, recall, steer, or receive notifications for the other's private data. Non-allowlisted users cannot enroll. Disabled users cannot continue through sessions, open streams, queued work, or approvals. Members cannot exercise administration through either HTTP, Telegram, or tool calls, including in `auto`. Existing data migrates only to the designated administrator. No user can accidentally authorize a personal Google account as Eggy's shared connection.

## Source references

- [Google OpenID Connect](https://developers.google.com/identity/openid-connect): server-side identity validation and stable subject identifiers.
- [Google web-server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server): authorization-code and offline access behavior.
- [Google OAuth overview](https://developers.google.com/identity/protocols/oauth2): external apps in Testing can receive seven-day refresh tokens for non-identity scopes. Document suitable consent-screen publishing configuration for the deployment; do not promise indefinite token life.
