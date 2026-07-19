# Cross-Calendar Reads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Calendar reads complete across every accessible calendar and every API result page.

**Architecture:** Add provider-neutral calendar discovery to the Calendar port, implement fully paginated Google CalendarList and Events reads, and aggregate readable calendars in the kernel service. Keep explicit single-calendar reads and all mutation approvals unchanged.

**Tech Stack:** Go 1.26, standard library HTTP/JSON, existing ports-and-adapters modules.

## Global Constraints

- Keep `internal/kernel` and `internal/ports` provider-neutral.
- Register tools only through `internal/bootstrap`.
- Do not weaken Calendar mutation approvals.
- Preserve `/data/state.json` schema compatibility.
- Add behavior test-first and run focused tests before the full matrix.

---

### Task 1: Paginated Google reads

**Files:**
- Modify: `internal/ports/ports.go`
- Modify: `internal/adapters/calendar/google/calendar.go`
- Test: `internal/adapters/calendar/google/google_test.go`

**Interfaces:**
- Produces: `CalendarInfo`, `CalendarProvider.ListCalendars(context.Context) ([]CalendarInfo, error)`
- Preserves: `CalendarProvider.List(context.Context, string, time.Time, time.Time) ([]CalendarEvent, error)`

- [ ] **Step 1: Write failing adapter tests**

Add a transport-backed test where CalendarList and Events each return `nextPageToken`; assert hidden calendars are requested and every item is returned.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/calendar/google -run 'TestAdapterListsAllCalendarsAndEventPages' -count=1`

Expected: compilation fails because `ListCalendars` does not exist.

- [ ] **Step 3: Implement the minimal adapter behavior**

Add `CalendarInfo` with ID, name, access role, primary, and hidden fields. Page through `GET /users/me/calendarList?showHidden=true`; page through event results with `pageToken`; close every response body before the next request.

- [ ] **Step 4: Run the focused adapter package**

Run: `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/adapters/calendar/google -count=1`

Expected: PASS.

### Task 2: Aggregate service and tool behavior

**Files:**
- Modify: `internal/kernel/services/calendar.go`
- Modify: `internal/bootstrap/assistant_tools.go`
- Test: `internal/kernel/services/calendar_test.go`
- Test: `internal/bootstrap/assistant_tools_test.go`

**Interfaces:**
- Consumes: `CalendarProvider.ListCalendars` and `CalendarProvider.List`
- Produces: `CalendarService.ListAll(context.Context, time.Time, time.Time) ([]CalendarEvent, error)`

- [ ] **Step 1: Write failing service and tool tests**

Assert aggregate reads skip `freeBusyReader`, merge readable calendars, sort by start time with deterministic tie-breakers, and that explicit `calendar_id` still performs one direct query.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/services ./internal/bootstrap -run 'Calendar.*All|CalendarList.*Across' -count=1`

Expected: compilation or assertion failure because aggregate reads do not exist.

- [ ] **Step 3: Implement the minimal aggregate path**

Implement `Calendars` and `ListAll`, register a read-only `calendar_calendars` metadata tool, update the `calendar_list` description, use aggregate reads only when `calendar_id` is omitted, and return `calendar_id: "all"` in that result envelope.

- [ ] **Step 4: Run focused packages**

Run: `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod go test ./internal/kernel/services ./internal/bootstrap -count=1`

Expected: PASS.

### Task 3: Documentation and verification

**Files:**
- Modify: `README.md`
- Modify: `config.example.yaml`

**Interfaces:**
- Documents: omitted `calendar_id` means all readable calendars; `default_calendar` remains the mutation default and explicit-read fallback configuration.

- [ ] **Step 1: Update operator documentation**

Describe aggregate reads, explicit calendar targeting, access-role limits, and pagination without changing setup requirements.

- [ ] **Step 2: Run formatting and the complete verification matrix**

Run: `GOCACHE=/tmp/eggy-go-cache GOMODCACHE=/tmp/eggy-go-mod make fmt vet test race build`

Expected: every target exits 0.

- [ ] **Step 3: Run Docker smoke when available**

Run: `make smoke`

Expected: PASS when the Docker daemon is available; otherwise report the daemon boundary explicitly.

- [ ] **Step 4: Check patch hygiene**

Run: `git diff --check`

Expected: exit 0 with no output.
