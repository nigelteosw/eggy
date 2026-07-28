package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	ErrUnknownTool = errors.New("model requested an unknown tool")
	// ErrToolStepLimit is the runaway guard, not a work cap: a turn that
	// keeps making progress compacts its context and continues, so reaching
	// this means the model is calling tools without ever answering.
	ErrToolStepLimit = errors.New("assistant tool-step limit reached")
)

type ModelTarget struct {
	Model   ports.Model
	ModelID string
}

// Event is one observable moment in a turn: the model spoke, a tool started,
// a tool returned, or a tool failed. There is one event stream for every
// turn, because there is one loop -- editing a repository is not a different
// kind of turn, it is a turn whose thread has a writable workspace attached.
type Event struct {
	Kind    string
	Call    ports.ToolCall
	Output  string
	Err     error
	Message ports.Message
}

const (
	EventAssistantMessage = "assistant_message"
	EventToolStart        = "tool_start"
	EventToolEnd          = "tool_end"
	EventToolError        = "tool_error"
)

type RunOptions struct {
	AllowedTools map[string]bool
	// OnEvent, if set, fires for every step of the turn. It is how a caller
	// renders live progress and records a transcript; the loop itself keeps
	// no history beyond the messages it sends to the model.
	OnEvent func(Event)
	// PendingInput, if set, is drained at each step boundary and appended to
	// the messages the next model call sees. It is how an owner steers a turn
	// that is already running: the message joins the turn in progress instead
	// of starting a competing one. The loop never blocks on it -- a turn with
	// nothing pending proceeds exactly as before.
	PendingInput func() []ports.Message
	// Transcript, if set, receives every event and every compaction
	// checkpoint durably. It is what makes a turn inspectable after the
	// process that ran it is gone.
	Transcript Transcript
}

type RunResult struct {
	Message ports.Message
	Usage   ports.ModelUsage
	// ReasoningContent is the chain-of-thought behind Message, from whichever
	// model turn produced the final answer, when the provider returns one.
	ReasoningContent string
}

type Loop struct {
	tools    map[string]ports.Tool
	defs     []ports.ToolDefinition
	selected map[string]ModelTarget
	policy   ContextPolicy
}

// NewSelectedLoop builds the one loop. policy is a context budget rather
// than a work cap: a turn that runs long compacts and continues.
func NewSelectedLoop(models map[string]ModelTarget, tools []ports.Tool, policy ContextPolicy) *Loop {
	registry := make(map[string]ports.Tool, len(tools))
	definitions := make([]ports.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		definition := tool.Definition()
		registry[definition.Name] = tool
		definitions = append(definitions, definition)
	}
	targets := make(map[string]ModelTarget, len(models))
	for alias, target := range models {
		targets[alias] = target
	}
	return &Loop{
		tools:    registry,
		defs:     definitions,
		selected: targets,
		policy:   policy.normalized(),
	}
}

// Run drives one turn to completion. There is exactly one termination
// condition: the model stops calling tools. Nothing designates a tool as
// terminal, because nothing distinguishes "the coding run finished" from
// "the model is done for now" -- shipping a change is an action the model
// takes mid-turn, whose result it reads and reports like any other.
//
// A tool that fails does not end the turn: the error is handed back as that
// tool's result so the model can react to it, which is the whole reason a
// failed patch is recoverable without a new run.
//
// Nor does running long end the turn. When the exchange the loop itself
// produced outgrows the context policy, the oldest steps are folded into a
// checkpoint summary the model keeps reading, and the turn continues -- the
// full sequence stays in the durable transcript. history and the owner's
// input are never compacted away: the instructions and the actual request
// are the last thing a long turn should lose.
func (l *Loop) Run(ctx context.Context, alias, effort, input string, history []ports.Message, options RunOptions) (RunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return RunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	definitions := l.filteredDefinitions(options)
	messages := append([]ports.Message(nil), history...)
	if input != "" {
		messages = append(messages, ports.Message{Role: ports.RoleUser, Content: input})
	}
	emit := func(event Event) {
		if options.OnEvent != nil {
			options.OnEvent(event)
		}
		if options.Transcript != nil {
			options.Transcript.Append(ctx, event)
		}
	}
	// preserved is everything the caller handed in: instructions, durable
	// context, recent conversation, and the request itself. Compaction only
	// ever touches what the loop appended after it.
	preserved := append([]ports.Message(nil), messages...)
	tail := []ports.Message(nil)
	summary := ""
	// checkpoint folds the oldest steps away when the live window is over
	// budget, and records the fact durably. It runs at a step boundary, so
	// no assistant message is ever separated from its tool results.
	checkpoint := func() {
		compacted, nextSummary, dropped := l.policy.compact(tail, summary)
		if !dropped {
			return
		}
		tail, summary = compacted, nextSummary
		if options.Transcript != nil {
			options.Transcript.Checkpoint(ctx, summary)
		}
	}
	live := func() []ports.Message {
		window := append([]ports.Message(nil), preserved...)
		if summary != "" {
			window = append(window, CheckpointMessage(summary))
		}
		return append(window, tail...)
	}
	result := RunResult{}
	steps := 0
	for {
		// The step boundary is also where a stopped turn actually stops.
		// Checking here rather than relying on the model adapter or a tool to
		// notice ctx means /stop is honoured even by a tool that ignores it.
		if err := ctx.Err(); err != nil {
			return result, err
		}
		// The step boundary is where steering lands: after any tool results
		// from the previous step are in the transcript, before the model is
		// asked what to do next.
		if options.PendingInput != nil {
			tail = append(tail, options.PendingInput()...)
		}
		// The step boundary is also the compaction checkpoint: the live
		// window is brought back inside its budget here, never mid-step.
		checkpoint()
		response, err := target.Model.Generate(ctx, ports.ModelRequest{Model: target.ModelID, Messages: live(), Tools: definitions, ReasoningEffort: effort})
		if err != nil {
			return result, err
		}
		result.Usage = result.Usage.Add(response.Usage)
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			result.Message = assistant
			result.ReasoningContent = response.ReasoningContent
			return result, nil
		}
		if l.policy.MaxSteps > 0 && steps >= l.policy.MaxSteps {
			return result, ErrToolStepLimit
		}
		tail = append(tail, assistant)
		emit(Event{Kind: EventAssistantMessage, Message: assistant})
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			if options.AllowedTools != nil && !options.AllowedTools[call.Name] {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			emit(Event{Kind: EventToolStart, Call: call})
			output, toolErr := tool.Execute(ctx, call.Arguments)
			kind := EventToolEnd
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
				kind = EventToolError
			}
			toolMessage := ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)}
			tail = append(tail, toolMessage)
			emit(Event{Kind: kind, Call: call, Output: string(output), Err: toolErr, Message: toolMessage})
		}
		steps++
	}
}

// ToolNames returns the tools available for a turn after applying the same
// allowlist filter used for the model request.
// ToolDefinitions is the exact tool set a turn run with options would send to
// the model. It is what /capabilities and /context report on, so the manifest
// stays "only the tools actually available to the current turn".
func (l *Loop) ToolDefinitions(options RunOptions) []ports.ToolDefinition {
	return l.filteredDefinitions(options)
}

func (l *Loop) ToolNames(options RunOptions) []string {
	definitions := l.filteredDefinitions(options)
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func (l *Loop) filteredDefinitions(options RunOptions) []ports.ToolDefinition {
	defs := append([]ports.ToolDefinition(nil), l.defs...)

	// Apply explicit tool allowlist.
	if options.AllowedTools != nil {
		filtered := make([]ports.ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if options.AllowedTools[d.Name] {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}

	return defs
}
