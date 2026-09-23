// Package bootstrap is the composition root: the one place that knows every
// adapter exists. It builds adapters from config, hands them to kernel
// services as ports, registers tools, and owns the event loop. It composes and
// nothing else; policy lives in internal/kernel, config parsing in
// internal/config, and HTTP routes in internal/web.
//
// Files:
//
//   - app.go                 App, AppOptions, and NewApp, the wiring itself
//   - app_wiring.go          stores, the model catalog, and gated tool registration
//   - events.go              event dispatch into the turn service
//   - run.go                 the daemon loop (Run) and the schedule tick
//   - heartbeat.go           the periodic unprompted check-in
//   - turn_presenter.go      how a turn shows typing and "Calling X..." on a surface
//   - routed_channel.go      delivering to whichever surface a turn came from
//   - telegram.go, discord.go, google.go, mcp.go, tavily.go
//     one file per optional integration; each returns nothing when unconfigured
//   - account_directory.go   the live list of accounts other packages read
//   - login.go, identity_link.go, connections.go
//     web sign-in, linking a chat identity to an account, and sealed chat credentials
//   - logging.go             the redacting, size-capped log files
package bootstrap
