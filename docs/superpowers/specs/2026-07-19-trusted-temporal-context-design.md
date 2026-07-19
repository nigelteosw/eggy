# Trusted Temporal Context Design

## Problem

Eggy currently asks the reasoning model to supply exact RFC3339 boundaries to `calendar_list` without giving it a trusted current date or timezone. The model can invent a stale date, receive a valid empty result, and incorrectly describe that range as “today.”

## Runtime context

Eggy will treat `calendar.timezone` as the owner's IANA timezone. If it is empty, Eggy will fall back to `scheduler.quiet_hours.timezone`. Startup will reject an invalid resolved timezone.

Every owner-message and heartbeat model run will receive a final volatile system message containing:

```text
Trusted temporal context
current_time: <RFC3339 timestamp in owner timezone>
timezone: <IANA timezone>
```

The hard policy will instruct the model never to infer the current date from memory or model knowledge and to use trusted temporal context or the time tool for relative dates.

## Time tool

A provider-neutral `current_time` tool will always be registered. It accepts an empty object and returns the same RFC3339 current time and IANA timezone. The clock is injected through bootstrap so tests remain deterministic.

## Calendar ranges

`calendar_list` will accept either:

- `range`: `today`, `tomorrow`, or `this_week`; or
- both explicit RFC3339 `from` and `to` values.

Mixing modes, omitting one explicit boundary, unknown range names, or a non-increasing interval returns a bounded validation error. `today` and `tomorrow` use local midnight boundaries in the owner timezone. `this_week` starts Monday at local midnight and ends the following Monday. Calendar boundaries use calendar arithmetic rather than fixed 24-hour durations so daylight-saving transitions remain correct.

The tool result will be an object containing `calendar_id`, `from`, `to`, `timezone`, and `events`. This makes the queried interval auditable before the model describes the result.

## Compatibility and safety

Existing explicit RFC3339 Calendar calls remain supported. Calendar mutation approvals are unchanged. No state schema, credential, provider, or repository behavior changes.

## Verification

Tests will cover deterministic prompt injection, the time tool, all relative ranges in a non-UTC timezone, explicit range compatibility, invalid mode combinations, and the exact interval returned to the model. Bootstrap integration will verify that the trusted timestamp appears in provider requests.
