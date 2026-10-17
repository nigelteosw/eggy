// Package marketplace installs skills and remote MCP servers from Claude Code
// plugin marketplaces and SKILL.md repositories on GitHub. Every install is an
// owner-reviewed, digest-bound plan pinned to a commit SHA: nothing is fetched
// into Eggy's skills directory until the owner has seen exactly what will be
// written. The package is a feature extension, enabled by marketplace.enabled.
package marketplace
