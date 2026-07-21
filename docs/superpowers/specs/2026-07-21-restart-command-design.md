# `/restart` Command Design

## Problem

`/config set provider|model|calendar` already writes validated changes to `config.yaml` and replies "Restart Eggy for this to take effect." — but there is no way to trigger that restart from Telegram. The owner must reach the host out-of-band (redeploy on Railway, or kill and re-run `./bin/eggyd` locally) to pick up the new config. Editing `config.yaml`/`.env` directly has the same gap.

## Design

### Restart mechanism: self-exec in place

`cmd/eggyd/main.go`'s `run()` already builds a cancellable context via `signal.NotifyContext` and, on cancellation, performs a graceful `server.Shutdown()` (15s timeout) before returning. `/restart` reuses this exact path rather than introducing a second shutdown mechanism:

1. `bootstrap.AppOptions` gains `RequestRestart func()`. `main.go` supplies an implementation that spawns a goroutine which sleeps ~1.5s, then calls the same `stop()` cancel function used for SIGTERM and sets a `restartRequested` flag.
2. `CommandService` gains a `restart func()` field, wired from `RequestRestart` in `NewApp`. The `/restart` case calls it and immediately returns `"Restarting Eggy to pick up config changes. Back in a few seconds."`. If `restart` is nil (fake-adapter/test wiring), it returns `"Restart is not available in this environment."` instead, matching the nil-guard pattern already used by `/continue`, `/config`, and `/usage`.
3. The 1.5s delay is what guarantees the confirmation message is actually delivered: `handleMessage` calls `commands.Execute` and then `channel.Deliver` on the same context, in sequence. If the restart callback cancelled that context synchronously, `Deliver`'s in-flight HTTP call to the Telegram API could be cancelled before it completes. Delaying cancellation gives `Deliver` (a single `sendMessage` call) ample time to finish first.
4. After `server.Shutdown()` completes in `main.go`, if `restartRequested` is set, instead of returning (process exit), `main.go` calls `syscall.Exec(exePath, os.Args, os.Environ())`, replacing the process image in place under the same PID. Execution re-enters `main()` from scratch: `.env` reloads via `bootstrap.DotEnv`, `config.yaml` reloads via `bootstrap.LoadOrCreateConfig`, and every provider/model/adapter rebuilds from the new values.

Because the PID is preserved, this behaves identically whether `eggyd` runs under `tini` in Docker/Railway or directly as `./bin/eggyd` locally — there is no dependency on Railway's `restartPolicyType = ON_FAILURE` policy, and no false "failure" entry in logs or alerting. This was checked against how comparable agent daemons handle it: Hermes Agent's gateway restart depends on an external supervisor noticing the process exit and relaunching it, which is a known source of bugs (the gateway sometimes exits and never comes back); self-exec avoids that class of failure entirely by never depending on anything external to notice.

### Scope boundaries

- No approval flow. `/restart` acts immediately, consistent with `/model`, `/clear`, and `/config set` — all Telegram commands are already gated by the owner allowlist upstream, so there is no additional actor to approve against.
- No special draining of in-flight coding-run subprocesses beyond what already exists for crash recovery (`CodingService.RecoverInterrupted`, `TaskService.RecoverInterrupted`, `Scheduler.Recover`). A restart is treated the same as any other unplanned stop for that purpose, which the system already tolerates.
- `syscall.Exec` is Unix-only, consistent with the rest of the runner/process-group code (`internal/adapters/runner/localprocess`), which is already Unix-only.
- Only `providers`/`models`/`calendar` config require a restart to take effect — `SOUL.md`/`USER.md`/`MEMORY.md` are already read fresh from disk on every message, so no lighter-weight in-process reload path is needed alongside this.

### Follow-up copy change

The three `/config set provider|model|calendar` success replies change from ending in `"Restart Eggy for this to take effect."` to `"Restart Eggy for this to take effect. Run /restart to apply now."`, so the command that creates the need for a restart also names the fix.

### Command registration

`/restart` is added to `telegram.Commands()` with a description (`"Restart Eggy to pick up config changes"`) so it appears in Telegram's autocomplete, and to the `CommandService.Execute` switch in `internal/bootstrap/commands.go`.

## Verification

- `internal/bootstrap/commands_test.go`: `/restart` calls the injected `restart` callback exactly once and returns the confirmation string; with `restart` left nil, it returns the "not available" message and does not panic; the three `/config set` success messages include the updated "Run /restart to apply now." text.
- `internal/adapters/channels/telegram/commands_test.go`: `Commands()` includes `restart` with a non-empty description.
- `main.go`'s exec-and-reload wiring is not unit-testable (it replaces the process image) and `cmd/eggyd` currently has no test file at all; it will be verified manually with `make build` followed by a local run: change a value in `config.yaml`, send `/restart`, and confirm the new value is active (via `/config get`) after the process comes back, with the same PID.
- Full repository check: `make fmt vet test race build`.
