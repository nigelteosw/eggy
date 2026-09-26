// Package web is Eggy's HTTP surface: the web panel's API, the browser chat,
// sign-in, first-run setup, and safe mode. Each route depends on a narrow
// interface declared beside it rather than on a whole service, and every
// config write goes through internal/config.
//
// Files, by area:
//
//   - server.go, web.go                 the top-level handlers, route table, and response helpers
//   - session.go, account_auth.go, login.go, login_link.go
//     sessions, password sign-in, throttling, and Telegram /web links
//   - accounts.go, identity_link.go     account management and linking chat identities
//   - chat.go                           threads, history, and the live chat stream
//   - config_routes.go, models.go, agent.go, mcp.go, discord.go
//     settings cards: config sections, model discovery, the model picker, MCP, Discord
//   - approvals.go, schedules.go, watch.go, tools.go, traces.go
//     one panel view each
//   - restart.go, safemode.go, setup.go restart, the repair page, and first-run setup
package panel
