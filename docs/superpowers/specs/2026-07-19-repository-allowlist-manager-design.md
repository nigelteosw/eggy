# Repository Allowlist Manager Design

## Goal

The trusted-repository allowlist currently lives only in `config.yaml`, requires shelling into the running container to edit, and needs a full service restart to take effect. Owners must be able to add or remove repositories from Telegram, with additions requiring the same approval discipline as commit/push/PR since they expand what the coding agent may autonomously touch. Scope is limited to how repository access is registered and managed; no changes to Calendar, model selection, or other adapters.

## Architecture

`ports.State` gains `Repositories map[string]ports.Repository`, populated from `config.yaml`'s `repositories:` list on first boot exactly like `SOUL.md`/`USER.md`/`MEMORY.md` are seeded once and never overwritten. After first boot, `config.yaml`'s `repositories:` key is inert; the map in `state.json` is the sole source of truth, loaded and mutated the same way `Schedules`, `CodingRuns`, and `Agent.SelectedModel` already are.

A new `internal/kernel/services/repositories.go` defines `RepositoriesService`, depending only on `ports.StateStore`, `ports.RepositoryProvider` (for a reachability check), and `ApprovalRequester` (the same interface `ShippingService` already uses). It does not depend on GitHub directly; a future non-GitHub adapter that implements `ports.RepositoryProvider` requires no change here.

- `Add(ctx, name, cloneURL, baseBranch, protectedBranches)`: rejects a duplicate name, rejects a malformed URL, runs a `git ls-remote` reachability check through the existing sanitized `ports.Runner`/askpash path used for clone/push, then stages an approval (`approvals.AddRepository`) carrying the proposed `ports.Repository` as payload. Nothing is persisted until approved.
- `Remove(ctx, name)`: applies immediately — refuses only if the repository has a coding run with `Status == "running"`. No approval required; removing only shrinks the trusted surface.
- `ExecuteApproved(ctx, approval)`: on `approvals.AddRepository`, re-validates the payload digest (same pattern as `ShippingService.Commit`/`Push`), writes the repository into `state.Repositories`.

### Extension points, not switch cases

`app.go`'s approval dispatcher currently hardcodes which service handles which action group in a `switch`. It's refactored once, as part of this feature, into:

```go
type ApprovalExecutor interface {
    ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error)
}
executors := map[approvals.Action]ApprovalExecutor{
    approvals.Commit: shipping, approvals.Push: shipping, approvals.CreatePR: shipping,
    approvals.CalendarCreate: calendar, approvals.CalendarUpdate: calendar, approvals.CalendarDelete: calendar,
    approvals.AddRepository: repositories,
}
```

This is the one deliberate edit to existing dispatch code. After it, registering a future new approval-gated capability is a map entry at the composition root, not a new `case` interleaved with existing branches. `ShippingService` and `CalendarService` themselves are untouched.

`commands.go`'s `/repositories` handling gains `add`/`remove` subcommand parsing inside its existing `case "/repositories":` branch, following the same pattern every other command already uses there. This file is the accepted, already-established extension point for new Telegram commands in this codebase (~15 commands today) and is intentionally not abstracted into a command registry — doing so would be the kind of DI/plugin framework `AGENTS.md` rules out for a surface this small.

`internal/kernel/services/repository_tools.go` (the LLM-facing `repository_list`/`repository_inspect`/`repository_modify` tools) is unchanged except that `repository_list` now reads from `state.Repositories` instead of the static config map. Adding/removing repositories stays a owner-issued Telegram command, never an LLM-invoked tool — the conversational model must not be able to expand its own trusted-repository surface.

## Commands

- `/repositories` — unchanged output, now reads live state.
- `/repositories add <name> <clone_url> [base_branch] [protected_branches]` — `base_branch` defaults to `main`. `protected_branches` is a single optional trailing argument, comma-separated (e.g. `main,release`), defaulting to `[base_branch]`; command parsing splits on whitespace via `strings.Fields` like every other command, so this keeps the argument count fixed at four rather than variadic. Validates, runs the reachability check, stages the approval, and delivers it through the existing Telegram approval-callback UI.
- `/repositories remove <name>` — applies immediately; returns an error naming the blocking run if one is active.

## Errors and Safety

Reuses `approvals.ErrPayloadChanged`/`ErrNotAuthorized`/`ErrExpired` verbatim — no new error taxonomy. A registered-but-unauthenticated Codex/provider state is out of scope here (tracked separately); this feature only governs which repositories exist in the allowlist. State schema gains one field (`Repositories`); existing `state.json` files without it default to an empty map, matching how `Schedules`/`CodingRuns` already handle absence.

**Correction from initial draft:** `ApprovalService`'s protected-branch check is currently a static `[]string` baked in once at `NewApprovalService(...)` construction, computed from `config.Repositories` at boot (`app.go:140-144`). That's incompatible with runtime-added repositories — a branch protected only on a repo added after boot would never be in that static list, silently weakening the "protected branches remain unpushable" guarantee. `ApprovalService.Authorize` changes to compute the protected set from live `state.Repositories` on every `Push` authorization instead (it already holds `ports.StateStore`), and the `protectedBranches []string` constructor parameter is removed.

## Verification

Unit tests for `RepositoriesService`: add success, duplicate name rejected, unreachable URL rejected, approval required before persistence, payload-digest mismatch rejected, remove blocked by an active run, remove succeeds otherwise. Command tests for `/repositories add`/`remove` argument parsing and error messages, mirroring existing `commands_test.go` style. Bootstrap test updated for the new executor map replacing the old switch. Completion requires `make fmt vet test race build`; `make smoke` runs when Docker is available.
