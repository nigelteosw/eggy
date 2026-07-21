# ADR 0004: `/restart` self-execs in place instead of relying on an external supervisor

## Context

`/config set provider|model|calendar` writes validated changes to
`config.yaml` and replies that Eggy needs a restart to pick them up, but
there was no way to trigger that restart from Telegram — the owner had to
reach the host out of band (redeploy on Railway, or kill and re-run
`eggyd` locally). A supervisor-relaunch design (exit the process and let
Railway's restart policy, or an external process manager, start it again) is
the more common shape for this, but it depends on something outside the
process noticing the exit and relaunching it — a known source of stuck
deployments in comparable agent daemons when that external step misfires.

## Decision

`/restart` reuses `cmd/eggyd/main.go`'s existing graceful-shutdown path
(`signal.NotifyContext` plus a 15s `server.Shutdown()`) rather than
introducing a second shutdown mechanism. It delays cancellation by ~1.5s so
the Telegram confirmation reply has time to actually send, then cancels the
same context SIGTERM uses. After `Shutdown()` completes, if a restart was
requested, `main.go` calls `syscall.Exec` on itself instead of returning —
replacing the process image in place under the same PID. Execution re-enters
`main()` from scratch, so `.env` and `config.yaml` reload and every
provider/model/adapter rebuilds from the new values.

## Consequences

Because the PID never changes, this behaves identically whether `eggyd`
runs under `tini` in Docker/Railway or directly as a local binary, with no
dependency on Railway's restart policy and no false "failure" entry in logs
or alerting from an expected exit. The trade-off is that this mechanism is
Unix-only (`syscall.Exec`), consistent with the rest of Eggy's
process/runner code, which already assumes a Unix host.
