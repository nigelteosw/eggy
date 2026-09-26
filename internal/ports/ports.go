// Package ports is the provider-neutral contract between the core and its
// adapters. The core depends only on these interfaces and types; every
// provider (Telegram, a model API, SQLite, GitHub, ...) implements them in its
// own internal/<family>/ package and is wired in by internal/bootstrap. Nothing here may
// import a provider package.
//
// The contract is split by topic:
//
//   - messages.go       what a turn exchanges: roles, content parts, messages
//   - model.go          Model, the model catalog, requests, responses, usage
//   - tools.go          Tool, its definition, and the effect it declares for the approval gate
//   - channels.go       Channel and its optional typing/progress/tracking extensions
//   - context.go        the owner-facing Markdown documents (soul, user, memory, watch list)
//   - conversations.go  stored messages, threads, and the workspace a thread has open
//   - skills.go         installed skills and their store
//   - state.go          durable runtime state, including the approval mode
//   - schedules.go      scheduled turns and messages
//   - repository.go     the runner and read-only repository access
//   - traces.go         per-turn traces of model and tool calls
//   - account.go        the acting account (Principal) carried on the context
//   - account_auth.go   account sign-in records
package ports
