// Package services holds the core services a turn is assembled from. Each is
// provider-neutral and reaches adapters only through internal/ports. Read-only
// repository and workspace tools live in the repo subpackage, which may import
// this one but never the reverse.
//
// Files:
//
//   - dispatcher.go          accepts each event once, for a known account, and routes it
//   - turns.go               which turns are running, and whether a new message waits, joins, or steers one
//   - conversation.go        recording and recalling the conversation
//   - agent_runtime.go, turn_model.go   the selected model and reasoning effort
//   - tools.go               the tool registry the loop asks for its tool set
//   - approval_gate.go       wraps each tool so a call is gated per the approval mode
//   - approval_service.go    raising, deciding, and authorizing approvals
//   - context.go             the memory tool, and the secret guard on durable writes
//   - memory_tools.go, schedule_tools.go, heartbeat_tools.go, skills_tools.go, time_tools.go
//     the native tools, one file each
//   - skills.go              listing and reading installed skills
//   - trace.go, traced_model.go   recording what each turn did, for the panel
package services
