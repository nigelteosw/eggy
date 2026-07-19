# Cross-Calendar Reads Design

## Goal

Calendar questions without an explicit calendar must include events from every non-hidden calendar in the authenticated user's Google Calendar list that grants event-read access, rather than silently reading only the configured primary calendar.

## Architecture

`internal/ports` gains provider-neutral calendar metadata and a calendar-discovery method. The Google adapter implements discovery through `users/me/calendarList`, excludes hidden entries, follows every `nextPageToken`, and follows every event-list `nextPageToken`. Provider credentials and Google response types remain inside the adapter.

The Calendar service exposes discovery and an aggregate read. The read-only `calendar_calendars` tool returns IDs, names, access roles, and primary status for non-hidden calendars so Eggy can answer calendar-discovery questions directly. Aggregate reads skip hidden calendars and calendars whose access role cannot reveal events, query each remaining readable calendar for the same interval, preserve the originating calendar ID on every event, and return a stable start-time ordering. An explicit calendar ID continues to query only that calendar. An omitted calendar ID requests the aggregate agenda and the tool result identifies the scope as `all`.

## Errors and Safety

Discovery or event-list failures fail the read rather than presenting a partial agenda as complete. Calendar mutations and their independent approvals are unchanged. The state schema and OAuth token format are unchanged. The already-requested `https://www.googleapis.com/auth/calendar` scope covers calendar-list discovery, so existing authorization remains usable.

## Verification

Adapter tests cover non-hidden calendar discovery, calendar-list pagination, and event pagination. Service and bootstrap tests cover filtering hidden and unreadable calendars, merging, sorting, explicit-calendar compatibility, and aggregate result metadata. Completion requires `make fmt vet test race build`; `make smoke` runs when Docker is available.
