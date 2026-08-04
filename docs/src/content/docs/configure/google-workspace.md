---
title: Google Workspace
description: Gmail, Calendar, Drive, Docs, Sheets and Contacts through one Google grant, authorized by pasting a redirect back.
eyebrow: Configure
---

One OAuth client, one consent screen, one stored grant, and a tool for each product configured. Absent unless configured: no token store is opened, no tool schema is built, and no scope is ever requested.

> **Trust model:** configuring Google authorizes the *reads*. Anything that writes — sending mail, changing an event, editing a sheet — stops and asks you first, per call, unless you say otherwise in [`require_approval`](#approvals). `/auto` turns every gate off at once, everywhere.

## Before you start

You need about ten minutes in the Google Cloud console, and shell access to set one environment variable on the deployment. You do **not** need a public address, a registered redirect URI, or a working callback route — that is the whole point of the client type used here.

## 1. Create a Google Cloud project

Open [console.cloud.google.com/projectcreate](https://console.cloud.google.com/projectcreate) and create a project. Any name; nothing depends on it. Select it in the console's project picker before continuing — every step below applies to the selected project.

## 2. Enable one API per product

Enable only what you plan to configure. A product whose API is off answers every call with a 403 naming itself, which Eggy passes through verbatim.

| Product | API to enable |
|---|---|
| Gmail | Gmail API |
| Calendar | Google Calendar API |
| Drive | Google Drive API |
| Docs | Google Docs API |
| Sheets | Google Sheets API |
| Contacts | People API |

Search each one in [the API library](https://console.cloud.google.com/apis/library) and press Enable. Or, with `gcloud` pointed at the project:

```bash
gcloud services enable \
  gmail.googleapis.com \
  calendar-json.googleapis.com \
  drive.googleapis.com \
  docs.googleapis.com \
  sheets.googleapis.com \
  people.googleapis.com
```

## 3. Configure the consent screen

Under **APIs & Services → OAuth consent screen**, choose:

- **Internal** if the account belongs to a Google Workspace organization. Prefer this — it has no test-user list and no verification.
- **External** otherwise. Add the owner's own Google account under **Test users**, or every authorization will be refused with `access_denied`.

Fill in an app name and a support email. Nothing else on this screen matters to Eggy.

> **External + Testing expires refresh tokens after seven days.** The grant will die weekly no matter how it was obtained, and this is not something Eggy can work around. Publish the app, or use an Internal client on a Workspace account.

## 4. Create a Desktop OAuth client

Under **APIs & Services → Credentials → Create credentials → OAuth client ID**, choose application type **Desktop app**.

This choice is the reason authorization works without any of the usual infrastructure. Google grants desktop clients an implicit loopback redirect, so:

- there is nothing to enter under Authorized redirect URIs;
- `server.public_base_url` plays no part in authorization;
- Eggy never has to be reachable from the browser you approve in.

A **Web application** client can be made to work — Google exempts localhost from its HTTPS rule, so `http://localhost:1` is a legal redirect URI to register — but it must be registered verbatim, and the match is exact down to the port. That makes port 1 load-bearing configuration in someone else's console: change `LoopbackRedirect` in the adapter and authorization breaks until the console is edited to match.

A Desktop client has no such entry, because loopback matching ignores the port. Keeping one client per integration also means a consent-screen or verification change to one does not move the other. If you already made a Web client for an MCP server, leave it alone.

Copy the client ID and client secret from the dialog.

## 5. Configure Eggy

Three ways in, all writing the same section through the same validation and the same file lock. Pick whichever surface you are already holding.

**From the web settings panel** — the Google Workspace card takes the client ID, the secret's variable name, and a checkbox per product. Nothing needs to be typed as YAML.

**From Telegram**, which is the only path that works when you have nothing but a phone:

```
/google set client_id=xxxx.apps.googleusercontent.com client_secret_env=GOOGLE_CLIENT_SECRET products=calendar,gmail
```

Both are upserts: fields you leave out keep their stored values, so `/google set enabled=false` turns Google off without erasing the client you would need to turn it back on. Neither surface can set `scopes`, `timeout`, or `max_output_bytes` — those are reviewed decisions and are preserved untouched by an edit from either.

**Or edit `config.yaml` directly:**

```yaml
google:
  enabled: true
  client_id: "xxxx.apps.googleusercontent.com"
  client_secret_env: "GOOGLE_CLIENT_SECRET"
  products: ["calendar", "gmail", "contacts"]
  scopes: []
  require_approval: ["gmail.send", "gmail.reply", "calendar.delete"]
  timeout: "30s"
  max_output_bytes: 131072
```

Set the secret in the deployment's environment:

```bash
GOOGLE_CLIENT_SECRET=<the client secret>
```

`EGGY_ENCRYPTION_KEY` must also be set — the grant is sealed with it in the same `auth.json` that holds MCP records.

Restart. Tools are built at startup, so a config edit takes effect on the next boot.

### Products

`calendar`, `contacts`, `docs`, `drive`, `gmail`, `sheets`. An unlisted product has no tool at all — no schema, no prompt bytes, no scope requested.

Adding a product later needs a fresh `/google login`: scopes are granted once for the whole client, not per product.

### Approvals

Leave `require_approval` out and every action that writes anything is gated — Eggy reads your mail without asking and sends none without asking you first. A gated call does not run: it puts an approve/reject prompt in front of you showing the tool and its arguments, and the result comes back to you rather than to the model.

| | Runs immediately | Asks first |
|---|---|---|
| `google_gmail` | `search`, `get`, `thread`, `labels`, `drafts`, `attachment` | `send`, `reply`, `draft`, `send_draft`, `modify`, `trash` |
| `google_calendar` | `list`, `calendars`, `get`, `freebusy` | `create`, `update`, `delete`, `respond` |
| `google_drive` | `search`, `get` | `create`, `update`, `delete`, `share` |
| `google_docs` | `get` | `create`, `append`, `replace` |
| `google_sheets` | `info`, `get` | `create`, `add_sheet`, `update`, `append`, `clear` |
| `google_contacts` | `list`, `search` | `create`, `update` |

Naming actions yourself **replaces** that default rather than adding to it:

```yaml
require_approval: ["gmail.send", "calendar.delete", "drive.share"]
```

That gates sending mail, cancelling events and handing out access, while letting Eggy draft, reply, relabel and move things around on its own. `"gmail.*"` gates a whole product; a bare `"gmail"` is refused, because it reads like "gate the writes" but would silently put a prompt in front of reading your inbox too — and a gate that fires on every read is one you learn to approve without looking. An empty list (`require_approval: []`) turns the gates off entirely; an unknown product or action fails at startup rather than presenting as a gate that quietly never fires.

Only tools with something gated carry the notice in their description, so gating nothing costs no prompt bytes.

Drafting is the pattern worth knowing: `gmail.draft` writes the mail into your Gmail drafts and sends nothing. Gate `send` and leave `draft` open and Eggy can compose freely while every outbound mail is still yours to approve.

### Scopes

Leave `scopes` empty and Eggy requests what the configured products need:

| Product | Default scope |
|---|---|
| Gmail | `gmail.modify` — read, send, and label |
| Calendar | `calendar` — read and write |
| Drive | `drive` — read and write |
| Docs | `documents` — read and write |
| Sheets | `spreadsheets` — read and write |
| Contacts | `contacts` — read and write |

Set `scopes` explicitly to narrow them: `drive.readonly`, `documents.readonly`, `contacts.readonly` and `calendar.readonly` all work, and are the supported way to run a read-only grant. The write actions then fail with Google's own 403 rather than disappearing — the tool is still advertised, because the scope is a property of your grant and not of the build.

Scopes are re-consented rather than widened in place, so decide before the first login.

> **Upgrading from a read-only grant:** Drive, Docs and Contacts used to request `.readonly` by default. If you authorized before they gained write actions, `/google status` will show the old scopes and every write will 403. Run `/google logout`, then `/google login` again to re-consent.

## 6. Authorize

From Telegram or the web chat:

```
/google login
```

Open the URL it returns and approve. **The browser will then fail to load the page it lands on.** That is expected and correct: the redirect points at `http://localhost:1`, where nothing is listening and nothing ever will be. The authorization code is sitting in that failed page's address bar.

Copy the whole address and send it back:

```
/google login http://localhost:1/?state=...&code=4/0AVG...&scope=...
```

A bare `code` value works too, if that is easier to copy on a phone.

The paste is accepted only against a login started within the last ten minutes, and the code is spent by the exchange — pasting the same one twice fails.

## 7. Confirm

```
/google
```

Reports whether the grant exists and, importantly, **which scopes were actually granted** — a consent screen lets an account untick individual permissions, and Eggy records what it received rather than what it asked for.

There are no per-product commands. The tools are on the registry from the next turn, and are reached in conversation:

> what's on my calendar tomorrow?
> any unread mail from the bank this week?

## Several calendars

A read with no calendar named covers **every calendar the account can see**, not just the primary one, and each event carries the calendar it came from. Calendars hidden in the Google UI are skipped, and at most twelve are read in one answer.

That needs no extra scope — `auth/calendar` already permits it — but a calendar only appears if the account is subscribed to it in Google Calendar itself. A shared calendar someone sent a link to, and which was never added, is not visible to the API either.

If one calendar cannot be read, the rest of the answer still comes back rather than the whole turn failing.

`/google logout` discards the grant.

## Times must carry a zone

Every time in `google_calendar` needs a UTC offset or `Z`:

```
2026-08-01T10:00:00-07:00      ✅
2026-08-01T10:00:00Z           ✅
2026-08-01T10:00:00            ❌ refused
```

A bare datetime is ambiguous and Google resolves it as UTC, which silently moves an event by hours. Eggy refuses it before anything is created. Relative phrasing in chat resolves against `agent.timezone`, so check that setting is right before letting Eggy create events.

## Guests are notified

An event created with `attendees` emails them, and deleting an event tells its guests it was cancelled. Google's default is the opposite — a meeting that exists only on your own calendar while the guests never hear about it, and which its own reference warns can fail to reach external calendars at all. Pass `send_updates: "none"` to change only your own calendar, or `"externalOnly"` for guests outside the organization. An event with no attendees notifies nobody regardless.

## Troubleshooting

| Message | Cause and fix |
|---|---|
| `not authorized — run /google login` | No grant stored, or it was revoked |
| `no pending Google login, or it expired` | More than ten minutes passed, or the code was already used. Run `/google login` again |
| `Google returned no refresh token` | The account has consented before. Revoke Eggy at [myaccount.google.com/permissions](https://myaccount.google.com/permissions) and authorize again |
| `Google token exchange failed: invalid_client` | The client secret does not belong to the client ID, or `GOOGLE_CLIENT_SECRET` is unset |
| `Google token exchange failed: invalid_grant` | The code expired or was already spent. Start over |
| `authorization was refused: access_denied` | External consent screen and the account is not on the test-user list |
| `authorization was refused: redirect_uri_mismatch` | `http://localhost:1` is not authorized on that client. Either add it verbatim under Authorized redirect URIs, or switch to a Desktop app client (step 4) |
| `has not been used in project … or it is disabled` | That product's API is not enabled (step 2) |
| A 403 naming a scope | The consent screen granted less than was asked for. `/google logout`, adjust `scopes`, log in again |
| Everything worked, then broke about a week later | External + Testing expires refresh tokens after seven days (step 3) |

## Relationship to MCP

This is a separate path from [MCP servers](/eggy/configure/mcp-servers), and the two do not share anything — not the client, not the token, not the redirect.

| | MCP server | Google Workspace |
|---|---|---|
| Client type | Web application | Desktop app |
| Redirect | `{public_base_url}/auth/mcp/{server}/callback`, registered in the console | `http://localhost:1`, registered nowhere |
| Needs a reachable address | Yes | No |
| Grants | One per server | One, covering every product |
| Contacts | Not available — Google hosts no People MCP server | Available |

If both are configured for the same product, you have two ways to reach one calendar. Pick one.
