---
title: Google Workspace
description: Gmail, Calendar, Drive, Docs, Sheets and Contacts through one Google grant.
---

One OAuth client, one consent, one stored grant, and a tool for each product configured. Absent unless configured: no token store is opened, no tool schema is built, and no scope is ever requested.

```yaml
google:
  enabled: true
  client_id: "xxxx.apps.googleusercontent.com"
  client_secret_env: "GOOGLE_CLIENT_SECRET"
  products: ["calendar", "gmail", "contacts"]
  scopes: []
  timeout: "30s"
  max_output_bytes: 131072
```

`client_id` is not a secret — it travels in the authorization URL — so it lives in YAML. `client_secret_env` names an environment variable; startup fails if the named variable is empty. The grant is sealed with `EGGY_ENCRYPTION_KEY` in the same `auth.json` that holds MCP records.

Products are `calendar`, `contacts`, `docs`, `drive`, `gmail`, and `sheets`. An unlisted product has no tool at all. Adding one later needs a fresh `/google login`, because scopes are granted once for the whole client rather than per product.

## Why a Desktop client

Create the credential in the Google Cloud console as an **OAuth client ID → Desktop app**, not a Web application.

Google grants desktop clients an implicit loopback redirect, so there is nothing to register under Authorized redirect URIs, nothing that has to be publicly reachable, and `server.public_base_url` plays no part in authorization. That is the whole difference from an MCP server's OAuth, where the callback must be registered byte for byte and the browser must be able to deliver to it.

Enable the APIs for the products you configured — Gmail, Calendar, Drive, Docs, Sheets, People — in the same project. An API that is off answers every call with a 403 naming itself, which Eggy passes through verbatim.

## Authorizing

```
/google login
```

Open the URL, approve, and the browser will fail to load the page it lands on. That is expected: the redirect points at `http://localhost:1`, where nothing is listening. The authorization code is in that failed page's address. Copy the whole address and send:

```
/google login <paste>
```

A bare `code` value works too. Either form is accepted only against a pending login started within the last ten minutes, and the code is spent by the exchange — a second paste of the same one fails.

`/google` reports whether the grant exists and which scopes were actually granted. `/google logout` discards it.

## Using it

There are no per-product commands. Once authorized, the tools are on the registry and reached in conversation:

> what's on my calendar tomorrow?
> any unread mail from the bank this week?

Every time in `google_calendar` must carry a timezone offset or `Z`. A bare datetime is read as UTC and lands hours away, so Eggy refuses it before creating anything.

## Failure modes

| Message | Fix |
|---|---|
| `not authorized — run /google login` | No grant stored, or it was revoked |
| `Google returned no refresh token` | Revoke Eggy at [myaccount.google.com/permissions](https://myaccount.google.com/permissions) and authorize again |
| `has not been used in project … or it is disabled` | Enable that product's API in the Cloud project |
| A 403 naming a scope | The consent screen granted less than was asked for; `/google logout`, adjust `scopes`, log in again |

If the consent screen is **External** and still in **Testing**, Google expires refresh tokens after seven days and the grant dies weekly no matter how it was obtained. Publish the app, or use an Internal client on a Workspace account.
