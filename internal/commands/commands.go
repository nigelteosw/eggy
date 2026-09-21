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
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
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
	Turns        TurnStopper
	Restarter    Restarter
	AgentRuntime AgentSettings
	DefaultModel string
	ModelAliases []string
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
	// aliasSet is ModelAliases as a lookup, so /model can tell a subcommand
	// from an alias that happens to share its name without a linear scan on
	// every invocation.
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
	default:
		return "Unknown command. Send /help for commands.", true, nil
	}
}

func (s *CommandService) status(ctx context.Context) (string, bool, error) {
	model := "unconfigured"
	if s.AgentRuntime != nil {
		selected, err := s.AgentRuntime.SelectedModel(ctx)
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
	if s.Store != nil {
		state, err := s.Store.Load(ctx)
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
	// The mode is always reported now that there are three of them. A bypass
	// the owner switched on days ago and forgot is still the failure mode worth
	// the line, but with a middle rung "no news" no longer means one specific
	// state, so saying nothing would leave the owner guessing which.
	if s.Approvals != nil {
		mode, err := s.Approvals.Mode(ctx)
		if err != nil {
			return "", true, err
		}
		report += "\n**Approvals:** " + ModeMessage(mode)
		if mode == ports.ModeAuto {
			report += " `/mode normal` restores the gate."
		}
	}
	if s.MCP != nil {
		statuses := s.MCP.Statuses()
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

// restartCommand is /restart. The work is in Restart below, which the panel's
// restart button calls too: one restart authority, two views onto it.
func (s *CommandService) restartCommand() (string, bool, error) {
	message, _ := Restart(s.Restarter, s.ConfigPath, s.Getenv)
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
