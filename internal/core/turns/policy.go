package turns

import (
	"strings"

	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Policy is what differs between kinds of turn: whether it carries ambient
// recent-conversation history, whether its exchange is recorded, and what to
// attribute the messages to.
type Policy struct {
	IncludeRecentHistory bool
	RecordConversation   bool
	Source               string
	// Extra are system messages appended after the shared instructions, for
	// rules that govern one kind of turn only. Keeping them here rather than in
	// agent.Instructions is what stops one turn kind's rules from riding along
	// on every other turn -- see agent.ScheduledTurnMessage.
	Extra []ports.Message
	// SuppressSilentReply lets a turn conclude there is nothing worth saying.
	// Only the heartbeat sets it: every other kind of turn answers something
	// the owner asked for and must always deliver.
	SuppressSilentReply bool
	// IncludeWatchDocument appends the watch list as a per-turn system
	// message. Only the heartbeat sets it: the document is the heartbeat's
	// own working memory and has no bearing on a turn the owner is present
	// for.
	IncludeWatchDocument bool
	// InputAlreadyRecorded marks a turn whose input is a message the
	// conversation already holds: a steer that its turn never got to read,
	// re-run as a turn of its own. Recording it a second time would show the
	// owner saying the same thing twice.
	InputAlreadyRecorded bool
	// Kind labels the turn in its trace: ports.TraceKindOwner,
	// TraceKindScheduled or TraceKindHeartbeat. It records which entry point
	// ran, which is the first thing worth knowing about a turn nobody
	// remembers asking for.
	Kind string
}

// ReadOnlyTools is the floor every restricted turn starts from.
//
// read_file and terminal resolve their workspace from session state, so a
// read-only turn needs workspace_open/close to have anything to read. Both
// stay read-only: an attached checkout has no branch, and the write
// primitives remain off this list.
func ReadOnlyTools() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true,
		"read_file": true, "repository_github": true,
		"workspace_open": true, "workspace_close": true,
		"skill_read": true,
		// Reading what is scheduled is read-only, and it is the one thing a
		// heartbeat most plausibly needs: it shares a clock with those jobs
		// and should be able to say what else is due. The grant is scoped to
		// the schedule tool's list action: create and cancel stay off, because
		// an unprompted turn must not change what runs later.
		"schedule:list": true,
	}}
}

// heartbeatTools is the read-only floor plus the heartbeat's own reply
// channel. heartbeat_respond stays out of ReadOnlyTools because a scheduled
// turn has no beat to end and must always deliver.
func heartbeatTools() agent.RunOptions {
	options := ReadOnlyTools()
	options.AllowedTools[services.HeartbeatRespondToolName] = true
	return options
}

// silentReply reports whether a heartbeat reply amounts to "nothing to say".
//
// A strict equality check is not enough: models reliably append a pleasantry
// to a sentinel, and "HEARTBEAT_OK -- all quiet!" on the owner's phone is the
// exact notification the heartbeat exists to avoid. So the token is
// recognised when it leads or trails the reply, stripped, and the reply
// dropped when what remains is short enough to be a pleasantry. The leniency
// only applies once the model has already declared nothing to report, so it
// cannot swallow a genuine short alert.
func silentReply(content string) bool {
	const pleasantryLimit = 300
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	for _, sentinel := range agent.HeartbeatSentinels {
		remainder, found := strings.CutPrefix(trimmed, sentinel)
		if !found {
			remainder, found = strings.CutSuffix(trimmed, sentinel)
		}
		if found && len(strings.TrimSpace(remainder)) <= pleasantryLimit {
			return true
		}
	}
	return false
}
