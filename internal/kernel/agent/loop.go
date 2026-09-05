package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	// AllowedTools names the tools a turn may reach. An entry may also be
	// scoped to one action of a multi-action tool as "name:action", which
	// grants only calls whose "action" argument matches. That is what lets a
	// read-only turn see what is scheduled without being able to change it,
	// now that the whole scheduling subject is one tool.
	AllowedTools map[string]bool
	// OnEvent, if set, fires for every step of the turn so a caller can render
	// live progress.
	OnEvent func(Event)
	// PendingInput, if set, is drained at each step boundary and appended to
	// the messages the next model call sees. It is how an owner steers a turn
	// that is already running: the message joins the turn in progress instead
	// of starting a competing one. The loop never blocks on it -- a turn with
	// nothing pending proceeds exactly as before.
	PendingInput func() []ports.Message
}

type RunResult struct {
	Message ports.Message
	Usage   ports.ModelUsage
	// ReasoningContent is the chain-of-thought behind Message, from whichever
	// model turn produced the final answer, when the provider returns one.
	ReasoningContent string
}

// ToolSource is where a turn's tools come from. There is exactly one per
// loop: the loop does not know, and must not know, that some of those tools
// arrive from a registry and others from a live MCP catalog. Composing those
// is the source's job -- see services.ToolRegistry, which holds the rule that
// a registered tool always wins a name collision.
//
// It is read once per turn, so a source whose contents change while the
// process runs (a reconnected MCP server, a reloaded catalog, a logout) takes
// effect on the next turn without rebuilding the loop.
type ToolSource interface {
	Tools() []ports.Tool
}

// StaticTools is a ToolSource over a fixed slice, for callers that have no
// live catalog at all.
type StaticTools []ports.Tool

func (s StaticTools) Tools() []ports.Tool { return s }

// ToolSourceFunc adapts a function to ToolSource.
type ToolSourceFunc func() []ports.Tool

func (f ToolSourceFunc) Tools() []ports.Tool { return f() }

type Loop struct {
	source   ToolSource
	selected map[string]ModelTarget
	policy   ContextPolicy
}

// NewSelectedLoop builds the one loop. policy is a context budget rather
// than a work cap: a turn that runs long compacts and continues.
func NewSelectedLoop(models map[string]ModelTarget, source ToolSource, policy ContextPolicy) *Loop {
	targets := make(map[string]ModelTarget, len(models))
	for alias, target := range models {
		targets[alias] = target
	}
	return &Loop{
		source:   source,
		selected: targets,
		policy:   policy.normalized(),
	}
}

// resolve snapshots the source. Callers take one snapshot per turn so a
// catalog that changes mid-turn cannot make a tool the model was just offered
// disappear before it is called.
//
// A duplicate name here resolves first-wins, but that is a backstop rather
// than the rule: the source is expected to have already settled precedence
// (ToolRegistry rejects duplicate registrations outright and drops a provider
// tool that collides with one).
func (l *Loop) resolve() (map[string]ports.Tool, []ports.ToolDefinition) {
	if l.source == nil {
		return nil, nil
	}
	available := l.source.Tools()
	tools := make(map[string]ports.Tool, len(available))
	definitions := make([]ports.ToolDefinition, 0, len(available))
	for _, tool := range available {
		definition := tool.Definition()
		if _, exists := tools[definition.Name]; exists {
			continue
		}
		tools[definition.Name] = tool
		definitions = append(definitions, definition)
	}
	return tools, definitions
}

// Run drives one turn to completion. The model finishes by returning a reply
// instead of another tool call; no tool has special termination semantics.
//
// A tool that fails does not end the turn: the error is handed back as that
// tool's result so the model can react to it, which is the whole reason a
// failed patch is recoverable without a new run.
//
// Nor does running long end the turn. When the exchange the loop itself
// produced outgrows the context policy, the oldest steps are folded into a
// checkpoint summary the model keeps reading, and the turn continues -- the
// history and the owner's input are never compacted away: the instructions
// and the actual request
// are the last thing a long turn should lose.
func (l *Loop) Run(ctx context.Context, alias, effort string, input ports.Message, history []ports.Message, options RunOptions) (RunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return RunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	tools, definitions := l.filteredTools(options)
	messages := append([]ports.Message(nil), history...)
	if strings.TrimSpace(input.Content) != "" || len(input.Parts) > 0 {
		input.Role = ports.RoleUser
		messages = append(messages, input)
	}
	emit := func(event Event) {
		if options.OnEvent != nil {
			options.OnEvent(event)
		}
	}
	// preserved is everything the caller handed in: instructions, durable
	// context, recent conversation, and the request itself. Compaction only
	// ever touches what the loop appended after it.
	preserved := append([]ports.Message(nil), messages...)
	tail := []ports.Message(nil)
	summary := ""
	// checkpoint folds the oldest steps away when the live window is over
	// budget. It runs at a step boundary, so
	// no assistant message is ever separated from its tool results.
	checkpoint := func() {
		compacted, nextSummary, dropped := l.policy.compact(tail, summary)
		if !dropped {
			return
		}
		tail, summary = compacted, nextSummary
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
		// from the previous step are in the live history, before the model is
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
			tool, ok := tools[call.Name]
			if !ok {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			if !allowsCall(options, call) {
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
	_, definitions := l.filteredTools(options)
	return definitions
}

func (l *Loop) ToolNames(options RunOptions) []string {
	_, definitions := l.filteredTools(options)
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

// filteredTools is the exact tool set one turn runs on: the live catalog
// narrowed by the turn's allowlist. Executable tools and the definitions sent
// to the model come from the same snapshot, so they can never disagree.
func (l *Loop) filteredTools(options RunOptions) (map[string]ports.Tool, []ports.ToolDefinition) {
	tools, defs := l.resolve()

	// Apply explicit tool allowlist.
	if options.AllowedTools != nil {
		filtered := make([]ports.ToolDefinition, 0, len(defs))
		for _, d := range defs {
			switch {
			case options.AllowedTools[d.Name]:
				filtered = append(filtered, d)
			case len(allowedActions(options, d.Name)) > 0:
				// Only some actions are granted, so the model is shown a
				// definition describing only those: a turn should never be
				// offered a capability its allowlist would then refuse.
				filtered = append(filtered, narrowDefinition(tools[d.Name], d, allowedActions(options, d.Name)))
			}
		}
		defs = filtered
	}

	return tools, defs
}

// ScopedTool is a tool whose actions can be granted one at a time. A tool that
// covers a whole subject behind an "action" argument implements it so a
// restricted turn can be handed the read-only slice of it and nothing else.
type ScopedTool interface {
	DefinitionForActions(actions []string) ports.ToolDefinition
}

func allowsCall(options RunOptions, call ports.ToolCall) bool {
	if options.AllowedTools == nil || options.AllowedTools[call.Name] {
		return true
	}
	action := callAction(call)
	if action == "" {
		return false
	}
	return options.AllowedTools[call.Name+":"+action]
}

// callAction reads the "action" argument a scoped grant is checked against.
// Arguments that do not parse have no action, and a scoped grant denies them.
func callAction(call ports.ToolCall) string {
	var arguments struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return ""
	}
	return strings.TrimSpace(arguments.Action)
}

func allowedActions(options RunOptions, name string) []string {
	actions := make([]string, 0, 2)
	for entry, allowed := range options.AllowedTools {
		if !allowed {
			continue
		}
		if action, found := strings.CutPrefix(entry, name+":"); found && action != "" {
			actions = append(actions, action)
		}
	}
	slices.Sort(actions)
	return actions
}

func narrowDefinition(tool ports.Tool, definition ports.ToolDefinition, actions []string) ports.ToolDefinition {
	if scoped, ok := tool.(ScopedTool); ok {
		return scoped.DefinitionForActions(actions)
	}
	return definition
}
