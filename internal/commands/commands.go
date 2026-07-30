// Package commands handles Telegram's deliberately small conversational
// command surface. Startup configuration and subsystem administration belong
// to config.yaml and the authenticated web panel, not to a shared command
// language.
package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

var telegramCommands = []struct {
	Name        string
	Description string
}{
	{Name: "help", Description: "Show the available commands"},
	{Name: "status", Description: "Show brief operational status"},
	{Name: "stop", Description: "Stop the current turn"},
	{Name: "clear", Description: "Clear recent conversation history"},
	{Name: "model", Description: "Show or select the active model"},
}

type ConversationResetter interface {
	Reset(ctx context.Context, conversationID string) error
}

type TurnStopper interface {
	Stop(ctx context.Context) bool
}

type AgentSettings interface {
	SelectedModel(context.Context) (string, error)
	SelectModel(context.Context, string) error
}

type Options struct {
	Store        ports.StateStore
	Conversation ConversationResetter
	Turns        TurnStopper
	AgentRuntime AgentSettings
	DefaultModel string
	ModelAliases []string
}

type CommandService struct {
	store        ports.StateStore
	conversation ConversationResetter
	turns        TurnStopper
	agentRuntime AgentSettings
	defaultModel string
	modelAliases []string
}

func New(options Options) *CommandService {
	aliases := append([]string(nil), options.ModelAliases...)
	sort.Strings(aliases)
	return &CommandService{
		store: options.Store, conversation: options.Conversation, turns: options.Turns,
		agentRuntime: options.AgentRuntime, defaultModel: options.DefaultModel, modelAliases: aliases,
	}
}

// Execute handles every slash command, including unknown ones. Ordinary prose
// returns handled=false and continues to the model.
func (s *CommandService) Execute(ctx context.Context, input string) (string, bool, error) {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", false, nil
	}
	name := strings.TrimPrefix(fields[0], "/")
	if at := strings.IndexByte(name, '@'); at >= 0 {
		name = name[:at]
	}
	args := fields[1:]
	switch name {
	case "help":
		return HelpText(), true, nil
	case "status":
		return s.status(ctx)
	case "stop":
		if s.turns == nil || !s.turns.Stop(ctx) {
			return "Nothing is running in this conversation.", true, nil
		}
		return "Stopping.", true, nil
	case "clear":
		if s.conversation == nil {
			return "Conversation history is unavailable.", true, nil
		}
		if err := s.conversation.Reset(ctx, destination.FromContext(ctx).ConversationID()); err != nil {
			return "", true, err
		}
		return "Cleared recent conversation history. Durable memory is unchanged.", true, nil
	case "model":
		return s.model(ctx, args)
	default:
		return "Unknown command.\n\n" + HelpText(), true, nil
	}
}

func (s *CommandService) status(ctx context.Context) (string, bool, error) {
	model := "unconfigured"
	if s.agentRuntime != nil {
		selected, err := s.agentRuntime.SelectedModel(ctx)
		if err != nil {
			return "", true, err
		}
		if selected != "" {
			model = selected
		}
	}
	pending := 0
	if s.store != nil {
		state, err := s.store.Load(ctx)
		if err != nil {
			return "", true, err
		}
		for _, approval := range state.Approvals {
			if approval.Status == approvals.Pending {
				pending++
			}
		}
	}
	return fmt.Sprintf("**Eggy status**\n\n**Active model:** %s\n**Pending approvals:** %d", model, pending), true, nil
}

func (s *CommandService) model(ctx context.Context, args []string) (string, bool, error) {
	if s.agentRuntime == nil {
		return "Model selection is unavailable.", true, nil
	}
	if len(args) == 0 {
		selected, err := s.agentRuntime.SelectedModel(ctx)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("**Active model:** %s\n**Available:** %s", selected, strings.Join(s.modelAliases, ", ")), true, nil
	}
	if len(args) != 1 {
		return "Usage: /model <alias>", true, nil
	}
	alias := args[0]
	if alias == "default" {
		alias = ""
	}
	if err := s.agentRuntime.SelectModel(ctx, alias); err != nil {
		return fmt.Sprintf("Could not select model: %v\n\nAvailable: %s", err, strings.Join(s.modelAliases, ", ")), true, nil
	}
	if alias == "" {
		alias = s.defaultModel
	}
	return "Model set to " + alias + ".", true, nil
}

func HelpText() string {
	return "Commands: /help, /status, /stop, /clear, /model [alias]"
}

func TelegramAutocomplete() []struct{ Name, Description string } {
	return append([]struct{ Name, Description string }(nil), telegramCommands...)
}
