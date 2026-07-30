package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

// SummaryLimit bounds the running compaction summary a turn carries.
const SummaryLimit = 4096

// ContextPolicy is the one budget a turn runs against. It replaces the fixed
// "how many tool steps fit in a turn" cap: the loop no longer ends a turn
// because it did a lot of work, it compacts the live message window and
// keeps going. The only termination condition is still that the model stops
// calling tools.
//
// BudgetChars and RecentSteps decide *when* to compact; MaxSteps stays only
// as a runaway guard against a model that calls tools forever without ever
// answering, which is a malfunction rather than a large piece of work.
type ContextPolicy struct {
	// BudgetChars bounds the characters of loop-generated messages (the
	// assistant/tool exchange) kept live in the model request.
	BudgetChars int
	// RecentSteps bounds how many tool-calling steps stay live before the
	// oldest are folded into the checkpoint summary.
	RecentSteps int
	// OutputExcerptChars bounds a single message's contribution to the
	// summary.
	OutputExcerptChars int
	// MaxSteps is the runaway guard. Zero means the default; a negative
	// value means no guard at all.
	MaxSteps int
}

func (p ContextPolicy) normalized() ContextPolicy {
	if p.BudgetChars <= 0 {
		p.BudgetChars = 96000
	}
	if p.RecentSteps <= 0 {
		p.RecentSteps = 16
	}
	if p.OutputExcerptChars <= 0 {
		p.OutputExcerptChars = 8192
	}
	if p.MaxSteps == 0 {
		p.MaxSteps = 500
	}
	return p
}

// step is one contiguous assistant-with-tool-calls message plus the tool
// results that answered it. Compaction drops whole steps, never a lone tool
// result: a provider rejects a tool message whose originating tool call is
// no longer in the live context.
type step struct {
	messages []ports.Message
	chars    int
}

// splitSteps groups messages into steps. A leading run of messages that is
// not introduced by an assistant message (only possible if a caller hands
// the loop a partial exchange) forms its own first group.
func splitSteps(messages []ports.Message) []step {
	steps := make([]step, 0, len(messages))
	for _, message := range messages {
		if message.Role == ports.RoleAssistant || len(steps) == 0 {
			steps = append(steps, step{})
		}
		current := &steps[len(steps)-1]
		current.messages = append(current.messages, message)
		current.chars += MessageChars([]ports.Message{message})
	}
	return steps
}

// compact folds the oldest steps of tail into summary until the live window
// fits the policy, keeping the most recent step whatever the budget says --
// a turn always sees the tool results it just produced. It returns the
// surviving tail, the extended summary, and whether anything was dropped.
func (p ContextPolicy) compact(tail []ports.Message, summary string) ([]ports.Message, string, bool) {
	steps := splitSteps(tail)
	dropped := false
	for len(steps) > 1 && (len(steps) > p.RecentSteps || stepChars(steps) > p.BudgetChars) {
		for _, message := range steps[0].messages {
			summary = AppendSummary(summary, SummarizeMessage(message))
		}
		steps = steps[1:]
		dropped = true
	}
	if !dropped {
		return tail, summary, false
	}
	kept := make([]ports.Message, 0, len(tail))
	for _, s := range steps {
		kept = append(kept, s.messages...)
	}
	return kept, summary, true
}

// CheckpointMessage renders the compaction checkpoint the model reads in
// place of the steps that were folded away. It is a system message rather
// than a fabricated assistant turn: the model must not mistake the summary
// for something it said.
func CheckpointMessage(summary string) ports.Message {
	return ports.Message{
		Role:    ports.RoleSystem,
		Content: "Checkpoint of earlier work in this turn (only this summary remains in live context):\n" + summary,
	}
}

func stepChars(steps []step) int {
	total := 0
	for _, s := range steps {
		total += s.chars
	}
	return total
}

// AppendSummary appends one bounded line to a running summary. It is shared
// by the loop's compaction checkpoint and the implementation session's
// resumable context so there is one definition of "what a folded-away step
// reads like".
func AppendSummary(summary, event string) string {
	event = TruncateRunes(strings.TrimSpace(event), 320)
	if event == "" {
		return summary
	}
	if summary == "" {
		return event
	}
	return TruncateRunes(summary+"\n"+event, SummaryLimit)
}

// SummarizeMessage renders the one line a message leaves behind once it is
// no longer live.
func SummarizeMessage(message ports.Message) string {
	if message.Name != "" {
		return "Used " + message.Name
	}
	if len(message.ToolCalls) > 0 {
		names := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			names = append(names, call.Name)
		}
		return "Called " + strings.Join(names, ", ")
	}
	if message.Content != "" {
		return TruncateRunes(message.Content, 160)
	}
	return "Recorded activity"
}

// TruncateMessage bounds a message's content for a summary.
func TruncateMessage(message ports.Message, limit int) ports.Message {
	message.Content = TruncateRunes(message.Content, limit)
	return message
}

// MessageChars counts a message window's contribution to the context budget,
// including tool-call arguments.
func MessageChars(messages []ports.Message) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
		for _, call := range message.ToolCalls {
			total += utf8.RuneCountInString(string(call.Arguments))
		}
	}
	return total
}

// TruncateRunes bounds value to limit runes; a non-positive limit is
// unbounded.
func TruncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
