# Guided Setup and Runtime Identity Design

## Purpose

A fresh Eggy should reach a usable chat without asking its operator to encode
accounts or Telegram sender IDs in environment-variable syntax. Environment
variables remain the boundary for secrets and deployment infrastructure;
owner-facing identity and capability choices belong in `config.yaml`, written
through `internal/config` from authenticated runtime surfaces.

## First-run experience

On a fresh home with no config or prior database/account artifacts, `eggyd`
enters setup mode instead of requiring
`EGGY_ACCOUNTS` or a legacy owner variable. Setup mode generates a random
32-byte token in memory and prints one URL whose token is in the URL fragment,
so it is not sent in the initial HTTP request or retained in access logs. The
web app exchanges that token once for an HttpOnly, SameSite=Strict setup cookie.
The token expires after 30 minutes and is regenerated on process restart.

The setup screen collects:

- the first account's immutable short ID and Google email;
- the public URL, prefilled from Railway or the current browser origin;
- the Google Web OAuth client ID and the environment-variable name for its secret;
- a model provider selection, credential variable name, model ID, and alias;
- optional Telegram enablement, using the deployment's existing bot credentials.

The operator provisions `EGGY_ENCRYPTION_KEY` and the sign-in client secret in
the deployment environment before completing setup. The screen accepts no
credential values and never generates, writes, or edits `.env`. It shows which
required variables are present, validates the candidate configuration against
the server-side environment, and atomically writes only `config.yaml` through
`internal/config`. Missing secrets are named without revealing their values;
the operator adds them through the hosting platform or an operator-managed
local `.env` and restarts Eggy when needed to reload the environment.
The supervisor gracefully
leaves setup mode and starts the ordinary app. If startup fails, the existing
safe mode remains the repair path.

Setup mode exposes only health, mode, setup-session, validation, and completion
routes. It does not open SQLite, construct a model, register tools, start
Telegram, or serve normal configuration endpoints. A configured capability
therefore still costs nothing until setup is completed.

## SQLite storage

Keep the existing `plugins/store/sqlite` adapter and one `<home>/eggy.db`.
SQLite is relational SQL storage. Its current WAL mode, FTS5 search, account
isolation, schema migrations, and bootstrap-owned lifetime remain in place.
Setup asks for no database choice or connection string. The separate PostgreSQL
draft is outside this delivery.

The Hermes example illustrates durable session/message storage in SQLite;
it does not require adopting Hermes's schema, per-profile databases, delegation,
provider-specific replay fields, or compaction machinery. Preserve Eggy's
existing storage contracts and add only the pairing records this flow needs.
YAML remains configuration, Markdown remains owner-facing documents, and
SQLite holds all machine-managed records.

## Runtime accounts

Accounts remain declarative YAML, with SQLite holding identity bindings and
sessions. The Accounts settings card continues to call the single mutation
authority in `internal/config`, but the running process resolves the account
directory from the validated document for every authorization decision.
Adding an account or changing an unbound account's email therefore takes effect
immediately. Removing an account continues to revoke its sessions and streams
before success is returned. Changes that alter an enrolled identity retain the
existing reset-binding safeguard.

No role or invitation subsystem is added. Every configured account retains the
same capabilities, and a verified Google identity may enroll only the account
whose configured email matches it.

## Telegram pairing

Telegram becomes explicitly enabled in YAML independently of whether an account
is paired. Its bot token and webhook secret remain environment secrets. When
enabled, bootstrap constructs the existing Telegram adapter. Explicit false
disables it; an absent enable flag preserves existing sender-based inference
for old configs. Fresh setup omits Telegram unless the operator enables it.

An authenticated account chooses **Link Telegram** in Settings. Eggy creates a
random, single-use pairing code, stores only its hash in SQLite with the account
ID and a ten-minute expiry, and shows a Telegram deep link. The existing webhook
accepts exactly one operation from an otherwise unmapped private sender:
`/start <pairing-code>`. It claims the record transactionally and calls an
`internal/config` mutation that assigns that numeric sender to the initiating
account, refusing an ID already assigned elsewhere. All other updates from
unmapped senders remain rejected. After the config write, finalize the claim;
a failed config write releases it. A crash leaves the claim unusable until the
owner generates a new code, because SQLite and YAML are not one transaction.
Generic selections and ordinary messages can
never pair an account.

The Telegram sender and chat resolvers read the validated live account
directory, so the newly paired sender works without restarting. Unlinking uses
the same config authority and takes effect immediately. Pairing codes and
attempts never enter conversation history or model prompts.

## Environment-variable boundary

First boot no longer uses `EGGY_ACCOUNTS`, `EGGY_OWNER_ID`, or
`EGGY_TELEGRAM_OWNER_ID` as the primary fresh-install interface. They remain a
documented compatibility/headless provisioning path. Secrets remain outside
YAML: encryption key, provider API keys, OAuth client secrets, Telegram bot
token, and webhook secret. Infrastructure injection such as `PORT`,
`RAILWAY_PUBLIC_DOMAIN`, `EGGY_HOME`, and `EGGY_CONFIG` remains unchanged.

Environment credentials belong to Eggy's deployment and its configured
capabilities, never to user enrollment:

| Category | Environment | Runtime configuration |
| --- | --- | --- |
| Deployment and sign-in | `EGGY_ENCRYPTION_KEY`, Google sign-in client secret | Public URL, login client ID, first account |
| Eggy's Telegram bot | `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET` | Enablement and each account's linked numeric sender |
| Optional extensions | GitHub token, model provider keys, MCP credentials | Enable/configure only the extensions needed |
| Eggy's Google Workspace identity | Outbound OAuth client secret | Expected Eggy email, products, connection status |

An extension credential is required only when its capability is configured.
GitHub, MCP, Telegram, and Workspace are not prerequisites for account setup;
the selected model credential is needed to start the current chat runtime.
Existing provider settings own model configuration; this plan adds no general
credential manager. A future supported bot adapter would use the same
environment boundary; this plan adds no Discord adapter.

Google Workspace belongs exclusively to Eggy's own provisioned Workspace user.
Its shared OAuth grant stays sealed in SQLite, and `google.expected_email`
continues to prevent connecting a person's Google account. Inbound Google
sign-in identifies Eggy's users and never grants access to their Workspace.
All users share what Eggy's Workspace identity can access.

This restriction is a security boundary: scope Workspace authorization to the
dedicated Eggy service identity and the resources explicitly made available to
it. Never request or retain grants for users' personal Workspace accounts.
Here, "service identity" means Eggy's dedicated ordinary Workspace user, as
required by `AGENTS.md`; it does not mean a Google Cloud IAM service account.
No service-account keys, domain-wide delegation, or user impersonation are
introduced. Keep identity verification before storing a grant and bind pending
approvals to the existing connection generation across reconnect/disconnect.

## Failure and recovery

- Missing or malformed input is reported per field before any file changes.
- A failed config write leaves the existing file untouched. Setup never changes
  the process environment, local `.env`, or hosting-platform secrets.
- Setup authorization is rate-limited and invalidated after completion.
- Replaying or guessing a Telegram pairing code does not reveal which account
  it named; expired and invalid codes receive the same Telegram response.
- Existing deployments skip setup mode and retain current startup behavior.
- Existing legacy and `EGGY_ACCOUNTS` first-boot deployments remain compatible.

## Testing and deletion budget

Tests cover setup-token secrecy and expiry, atomic file creation, secret
redaction, immediate account authorization changes, pairing replay/expiry,
duplicate Telegram IDs, private-chat enforcement, and existing-deployment
compatibility. The final gate is `make fmt vet test race build`; `make smoke`
runs when Docker is available.

The change removes the ordinary user's need to author `EGGY_ACCOUNTS` and paste
Telegram IDs, but retains their parsers for compatibility. It adds no durable
form, framework, background loop, native tool, model schema, role system, or
second config writer. Production additions are limited to one setup HTTP
surface, one SQLite pairing record, live config-backed account resolution, and
their UI. Delete the setup HTTP surface if first-run provisioning returns to a
headless-only product; delete pairing records and routes if Telegram pairing is
removed.
