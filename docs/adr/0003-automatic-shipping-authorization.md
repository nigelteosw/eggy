# ADR 0003: Automatic shipping, with independent authorization checks preserved

## Context

Eggy originally required a separate owner Telegram tap to approve each of
commit, push, and pull-request creation after a repository run finished —
three manual decisions per shipped change, on top of whatever review the
owner does on the resulting pull request itself. `ShippingService` also
carried this as three separately setter-injected roles (`policy`,
`requester`, `decider`), plus compatibility branches for a Telegram callback
executor map left over from that manual-tap flow.

## Decision

`ShippingService.Ship` runs commit, push, and pull-request creation back to
back, deciding each step's approval itself instead of waiting for an owner
tap. This does not remove the approval records or their checks — it removes
only the requirement that a human tap each one. Each step still creates a
distinct, expiring, payload-digest-bound `approvals.Approval`
(`ApprovalService.Authorize` rejects a changed payload or an expired
approval), local and remote HEAD are revalidated immediately before push and
before pull-request creation, and a protected branch is still denied at push
time regardless of approval status. Eggy still never merges a pull request.
All required dependencies are now constructor-injected rather than supplied
through partial-construction setters, and the Telegram callback-executor
compatibility path for manual shipping taps is gone.

Calendar create/update/delete and repository registration (`add_repository`)
are deliberately not part of this decision — those keep an explicit owner
Telegram tap, because they are infrequent, externally visible mutations
where a brief pause has low cost, unlike a shipping chain that already ends
in an owner-reviewed pull request.

## Consequences

A validated implementation run reaches an open pull request without a
round-trip through Telegram, at the cost of removing the one point where an
owner could have rejected a specific step (commit, or push, or PR) before it
happened rather than after, by rejecting the pull request itself. The
authorization plumbing that made rejection meaningful — digest binding,
expiry, protected-branch denial, HEAD revalidation — is exactly what is
preserved, so the automatic path cannot silently skip a check a manual tap
would have gone through.
