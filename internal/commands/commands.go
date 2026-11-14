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
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ConversationResetter interface {
	Reset(ctx context.Context, conversationID string) error
}

type TurnStopper interface {
	Stop(ctx context.Context) bool
}

type AgentSettings interface {
	SelectedModel(context.Context) (string, error)
	SelectModel(context.Context, string) error
	ReasoningEfforts(string) []string
	ReasoningEffort(context.Context) (string, error)
	SelectReasoningEffort(context.Context, string) error
	ShowThinking(context.Context) (bool, error)
	SetShowThinking(context.Context, bool) error
	Heartbeat(context.Context) (bool, error)
	SetHeartbeat(context.Context, bool) error
	Theme(context.Context) (string, error)
	SetTheme(context.Context, string) error
	Look(context.Context) (ports.LookView, error)
	SetLook(context.Context, ports.LookChange) error
}

// ApprovalGate is the runtime switch behind /mode. Reading and writing go
// through the one approval authority rather than this package touching state
// directly, so the gate has a single answer whoever asks.
type ApprovalGate interface {
	Mode(context.Context) (ports.ApprovalMode, error)
	SetMode(context.Context, ports.ApprovalMode) error
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
	// Code is the agent runner for /code. Nil means the extension is off, and
	// /code says so rather than failing.
	Code      CodeRunner
	Turns     TurnStopper
	Restarter Restarter
	// RestartPending reports whether config.yaml differs from what this process
	// loaded. Nil means the process cannot tell, so /status says nothing about it.
	RestartPending func() bool
	AgentRuntime   AgentSettings
	// Soul reads SOUL.md for /soul.
	Soul SoulReader
	// HeartbeatCadence describes when beats run, such as "every 3h", or is
	// empty when the deployment has no runnable heartbeat -- which /heartbeat
	// on says, rather than implying a switch alone makes Eggy check in.
	HeartbeatCadence string
	DefaultModel     string
	ModelAliases     []string
	// ModelDiscovery browses a provider's catalog for /model available. Nil
	// leaves that subcommand saying so, which is what a deployment whose
	// providers all opted out of discovery gets.
	ModelDiscovery ModelDiscoverer
	// PublicBaseURL is where the owner's browser reaches this deployment
	// (server.public_base_url). /web has nothing to hand out without it.
	PublicBaseURL string
	// WebLoginLink mints a single-use browser login link for the verified
	// chat sender on ctx, acting as the principal on ctx. Wired only by
	// bootstrap, and called only when the context carries a sender the
	// Telegram webhook verified; nil leaves /web sending the bare address.
	WebLoginLink func(ctx context.Context, senderID string) (string, error)
	// Now is the clock the login link's expiry is measured against. Defaults
	// to time.Now.
	Now func() time.Time
	// Getenv is the daemon's own environment lookup, including .env, which
	// the process environment does not carry. /restart pre-flights the config
	// with it so the check answers the same question startup will.
	Getenv func(string) string
}

// CommandService embeds Options rather than restating every collaborator as a
// lowercase field and copying them across one at a time: the two lists were
// identical apart from case, so the copy only bought a third place to forget a
// field when adding one. aliasSet is the exception -- it is derived from
// ModelAliases rather than supplied, so it stays a field of its own.
type CommandService struct {
	Options
	// aliasSet is ModelAliases as a lookup, so the model tool can tell a
	// loaded alias from one only written to config without a linear scan.
	aliasSet map[string]struct{}
}

func New(options Options) *CommandService {
	if options.Now == nil {
		options.Now = time.Now
	}
	// Normalized in place, so the stored copy is the one every command reads
	// and there is no second, unsorted spelling of the same list.
	options.ModelAliases = append([]string(nil), options.ModelAliases...)
	slices.Sort(options.ModelAliases)
	options.PublicBaseURL = strings.TrimRight(strings.TrimSpace(options.PublicBaseURL), "/")
	aliasSet := make(map[string]struct{}, len(options.ModelAliases))
	for _, alias := range options.ModelAliases {
		aliasSet[alias] = struct{}{}
	}
	return &CommandService{Options: options, aliasSet: aliasSet}
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
	for _, command := range telegramCommands {
		if command.Name == name && command.NoArgs && len(args) != 0 {
			return "Usage: /" + name, true, nil
		}
	}
	switch name {
	case "menu", "settings":
		panel, err := s.Control(ctx, name)
		return panel.Text, true, err
	case "start":
		// Only a linked sender reaches commands, so a /start carrying a
		// pairing token is someone already linked opening another link. It
		// is not redeemed: this chat stays bound as it is.
		if len(args) > 0 {
			return "This Telegram is already linked to your Eggy account, so that link was not used. To link a different account, unlink this one in the web panel first.", true, nil
		}
		return "Hi, I'm Eggy. Send me anything, or use /menu for the controls. /web opens the web panel with a one-tap sign-in link.", true, nil
	case "help":
		return s.help(args), true, nil
	case "status":
		return s.status(ctx)
	case "stop":
		if s.Turns == nil || !s.Turns.Stop(ctx) {
			return "Nothing is running in this conversation.", true, nil
		}
		return "Stopping.", true, nil
	case "clear":
		if s.Conversation == nil {
			return "Conversation history is unavailable.", true, nil
		}
		if err := s.Conversation.Reset(ctx, destination.FromContext(ctx).ConversationID()); err != nil {
			return "", true, err
		}
		return "Cleared recent conversation history. Durable memory is unchanged.", true, nil
	case "model":
		return s.model(ctx, args)
	case "mcp":
		return s.mcpCommand(ctx, args)
	case "web":
		return s.webCommand(ctx)
	case "google":
		return s.googleCommand(ctx, args)
	case "mode":
		if len(args) > 1 {
			return modeUsage, true, nil
		}
		return s.modeCommand(ctx, strings.Join(args, " "))
	case "restart":
		return s.restartCommand()
	case "soul":
		return s.soulCommand(ctx)
	case "heartbeat":
		return s.heartbeatCommand(ctx, args)
	case "code":
		return s.codeCommand(ctx, args)
	default:
		return "Unknown command. Send /help for commands.", true, nil
	}
}

// restartCommand is /restart. The work is in Restart below, which the panel's
// restart button calls too: one restart authority, two views onto it.
func (s *CommandService) restartCommand() (string, bool, error) {
	message, _ := Restart(s.Restarter, s.ConfigPath, s.Getenv)
	return message, true, nil
}

// RestartMessage is the one wording for a restart that was accepted, so chat
// and the panel cannot describe the same event two different ways.
const RestartMessage = "Restarting. Config is reloaded from disk; anything running finishes first, and I'm back in a few seconds."

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
