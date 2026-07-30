---
title: Google Calendar
description: Enable Eggy's native Calendar tools, connect OAuth, and preserve per-event mutation approvals.
eyebrow: Configure
---

Google Calendar is Eggy's one compiled-in product integration. It remains native because Calendar mutations require an approval boundary that configured MCP servers do not provide.

## Enable Calendar

```yaml
calendar:
  default_calendar: primary
```

Set:

```text
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
EGGY_ENCRYPTION_KEY
```

Then visit:

```text
https://your-eggy.example/auth/google
```

The callback is `/auth/google/callback`. OAuth records are encrypted at rest with AES-256-GCM.

## Read tools

`calendar_calendars` lists non-hidden readable calendars. `calendar_list` reads events; when `calendar_id` is omitted, it aggregates across all non-hidden calendars that allow event reads.

Ranges `today`, `tomorrow`, and `this_week` are resolved by Eggy's clock in `agent.timezone`. Explicit RFC3339 `from` and `to` values are also accepted.

## Mutation tools

`calendar_create`, `calendar_update`, and `calendar_delete` always return `awaiting_owner`. The change happens only after approval of that exact normalized payload.

`calendar.default_calendar` supplies the target when a mutation omits `calendar_id`. It does not restrict reads to that calendar.

## Disable Calendar

Remove the section or leave `default_calendar` empty. Eggy then registers no Calendar tools, exposes no Google OAuth routes, and adds no Calendar capability text to the model prompt.
