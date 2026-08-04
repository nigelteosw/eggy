// Package commands handles Telegram's deliberately small conversational
// command surface. Most startup configuration belongs to config.yaml and the
// authenticated web panel rather than to a chat command language. MCP is the
// exception, because the config it edits lives on the Eggy runtime: an owner
// holding a phone must be able to add or authorize a server without shelling
// into the deployment. Those edits go through the internal/config helpers the
// web panel calls, under the same lock and the same validation -- one
// administration authority, two views onto it.
package commands

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
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
	{Name: "mcp", Description: "List, configure, and authorize MCP servers"},
	{Name: "google", Description: "Authorize Google Workspace and show its status"},
	{Name: "auto", Description: "Switch auto mode on or off for approval-gated tool calls"},
	{Name: "restart", Description: "Reload config.yaml by rebuilding the running daemon"},
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

// ApprovalGate is the runtime switch behind /approvals. Reading and writing go
// through the one approval authority rather than this package touching state
// directly, so the gate has a single answer whoever asks.
type ApprovalGate interface {
	AutoApprove(context.Context) (bool, error)
	SetAutoApprove(context.Context, bool) error
}

// Restarter rebuilds the daemon around a freshly read config.yaml. It is the
// chat-side answer to every "restart Eggy for this to take effect" notice this
// package and the web panel print: config written from a phone should be
// applicable from a phone, without shelling into the deployment. Restart must
// not block, because the turn calling it still owes the owner a reply.
type Restarter interface {
	Restart()
}

type Options struct {
	ConfigPath   string
	MCP          MCPRuntime
	Google       GoogleRuntime
	Store        ports.StateStore
	Approvals    ApprovalGate
	Conversation ConversationResetter
	Turns        TurnStopper
	Restarter    Restarter
	AgentRuntime AgentSettings
	DefaultModel string
	ModelAliases []string
	// Getenv is the daemon's own environment lookup, including .env, which
	// the process environment does not carry. /restart pre-flights the config
	// with it so the check answers the same question startup will.
	Getenv func(string) string
}

type CommandService struct {
	configPath   string
	mcp          MCPRuntime
	google       GoogleRuntime
	store        ports.StateStore
	approvals    ApprovalGate
	conversation ConversationResetter
	turns        TurnStopper
	restarter    Restarter
	getenv       func(string) string
	agentRuntime AgentSettings
	defaultModel string
	modelAliases []string
}

func New(options Options) *CommandService {
	aliases := append([]string(nil), options.ModelAliases...)
	slices.Sort(aliases)
	return &CommandService{
		configPath: options.ConfigPath, mcp: options.MCP, google: options.Google,
		store: options.Store, approvals: options.Approvals, conversation: options.Conversation, turns: options.Turns,
		restarter: options.Restarter, getenv: options.Getenv,
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
	case "mcp":
		return s.mcpCommand(ctx, args)
	case "google":
		return s.googleCommand(ctx, args)
	case "auto":
		return s.autoCommand(ctx)
	case "restart":
		return s.restartCommand()
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
	// A bare count answers "is anything waiting" but not "waiting for what",
	// which is the question the owner actually has, so each pending approval
	// is named by its summary.
	waitingApprovals := []approvals.Approval(nil)
	if s.store != nil {
		state, err := s.store.Load(ctx)
		if err != nil {
			return "", true, err
		}
		for _, approval := range state.Approvals {
			if approval.Status == approvals.Pending {
				waitingApprovals = append(waitingApprovals, approval)
			}
		}
	}
	// Oldest first: state stores approvals in a map, so without this the order
	// would differ between two runs of the same command.
	slices.SortFunc(waitingApprovals, func(a, b approvals.Approval) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	pending := len(waitingApprovals)
	waiting := make([]string, 0, pending)
	for _, approval := range waitingApprovals {
		summary := approval.Summary
		if strings.TrimSpace(summary) == "" {
			summary = string(approval.Action)
		}
		waiting = append(waiting, fmt.Sprintf("- %s (`%s`, requested %s)", summary, approval.ID, approval.CreatedAt.Format("2 Jan 15:04")))
	}
	report := fmt.Sprintf("**Eggy status**\n\n**Active model:** %s\n**Pending approvals:** %d", model, pending)
	if len(waiting) > 0 {
		report += "\n" + strings.Join(waiting, "\n")
	}
	// Reported unconditionally when on, and only when on. A bypass the owner
	// switched on days ago and forgot is the failure mode worth a line here;
	// the safe state does not need one.
	if s.approvals != nil {
		auto, err := s.approvals.AutoApprove(ctx)
		if err != nil {
			return "", true, err
		}
		if auto {
			report += "\n**Auto mode is enabled** — gated tool calls run without asking. `/auto` switches it back off."
		}
	}
	if s.mcp != nil {
		statuses := s.mcp.Statuses()
		ready, tools, attention := 0, 0, []string(nil)
		for _, status := range statuses {
			tools += status.Tools
			if status.State == mcpStateReady {
				ready++
				continue
			}
			attention = append(attention, status.Name+" ("+status.State+")")
		}
		report += fmt.Sprintf("\n**MCP:** %d/%d ready, %d tools", ready, len(statuses), tools)
		if len(attention) > 0 {
			report += "\n**Needs attention:** " + strings.Join(attention, ", ")
		}
	}
	return report, true, nil
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

// autoCommand flips auto mode. It takes no argument on purpose: a toggle the
// owner can fire from a phone without remembering a subcommand, and the reply
// states the resulting mode so the tap is always confirmed rather than
// assumed.
func (s *CommandService) autoCommand(ctx context.Context) (string, bool, error) {
	if s.approvals == nil {
		return "Approvals are unavailable.", true, nil
	}
	auto, err := s.approvals.AutoApprove(ctx)
	if err != nil {
		return "", true, err
	}
	if err := s.approvals.SetAutoApprove(ctx, !auto); err != nil {
		return "", true, err
	}
	return AutoModeMessage(!auto), true, nil
}

// restartCommand is /restart. The work is in Restart below, which the panel's
// restart button calls too: one restart authority, two views onto it.
func (s *CommandService) restartCommand() (string, bool, error) {
	message, _ := Restart(s.restarter, s.configPath, s.getenv)
	return message, true, nil
}

// RestartMessage is the one wording for a restart that was accepted, so chat
// and the panel cannot describe the same event two different ways.
const RestartMessage = "Restarting. Config is reloaded from disk; anything running finishes first, and Eggy is back in a few seconds."

// Restart rebuilds the daemon so a config.yaml edited from the panel or by
// /mcp takes effect, reporting what to tell the owner and whether it is
// happening.
//
// The load is a pre-flight, not a formality: a config that fails to load puts
// the process into safe mode, where Telegram is gone and only the repair page
// can reach it. Refusing here leaves the owner holding a working Eggy and the
// reason the new file would not have started. getenv is the daemon's own
// lookup so the check sees .env, exactly as startup will; nil falls back to
// the process environment.
func Restart(restarter Restarter, configPath string, getenv func(string) string) (string, bool) {
	if restarter == nil {
		return "Restarting is unavailable.", false
	}
	if configPath != "" {
		if getenv == nil {
			getenv = os.Getenv
		}
		if _, _, err := config.LoadConfig(configPath, getenv); err != nil {
			return fmt.Sprintf("Not restarting: config.yaml would not load.\n\n%v", err), false
		}
	}
	restarter.Restart()
	return RestartMessage, true
}

// AutoModeMessage is the one wording for the switch's new state, so Telegram
// and the web panel cannot describe the same setting two different ways.
func AutoModeMessage(auto bool) string {
	if auto {
		return "Auto mode enabled. Approval-gated tool calls now run without asking."
	}
	return "Auto mode disabled. Approval-gated tool calls will ask before running."
}

func HelpText() string {
	return "Commands: /help, /status, /stop, /clear, /model [alias], /mcp [add|remove|enable|disable|login|logout], /google [login|logout], /auto, /restart"
}

func TelegramAutocomplete() []struct{ Name, Description string } {
	return append([]struct{ Name, Description string }(nil), telegramCommands...)
}
