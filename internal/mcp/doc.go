// Package mcp connects Eggy to configured MCP servers and offers their tools
// to the loop.
//
// manager.go connects, discovers, filters, reconnects, and reports each
// server's status; session.go opens HTTP or stdio sessions (process_*.go
// manage the stdio child process); tool.go and result.go adapt a remote tool
// and its results; oauth.go and oauth_store.go handle servers that need
// authorization; config.go, names.go, and types.go hold server settings, tool
// naming, and shared types; and fake.go is the
// catalog-only fake used when Eggy runs with fake adapters.
package mcp
