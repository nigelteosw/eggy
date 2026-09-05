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
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/auth/session"
)

// webLoginLinkTTL matches internal/web's own expiry for the link it accepts.
// The two are stated separately rather than shared, because a command package
// importing the HTTP server to read one duration would invert the dependency
// the whole package layout rests on.
const webLoginLinkTTL = 5 * time.Minute

var telegramCommands = []struct {
	Name        string
	Description string
}{
	{Name: "help", Description: "Show the available commands"},
	{Name: "status", Description: "Show brief operational status"},
	{Name: "stop", Description: "Stop the current turn"},
	{Name: "clear", Description: "Clear recent conversation history"},
	{Name: "model", Description: "Show or select the active model; browse and add with providers/available/add"},
	{Name: "mcp", Description: "List, configure, and authorize MCP servers"},
	{Name: "web", Description: "Send a one-tap sign-in link to the web panel"},
	{Name: "google", Description: "Authorize Google Workspace and show its status"},
	{Name: "mode", Description: "Show or set how much Eggy asks before tool calls: strict, normal or auto"},
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
	// SigningKey signs the one-tap login link /web sends, the same key the
	// web panel signs session cookies with. Empty leaves /web sending a bare
	// URL and an instruction to log in by hand.
	SigningKey []byte
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
	switch name {
	case "help":
		return HelpText(), true, nil
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
		return s.webCommand()
	case "google":
		return s.googleCommand(ctx, args)
	case "mode":
		return s.modeCommand(ctx, strings.Join(args, " "))
	case "restart":
		return s.restartCommand()
	default:
		return "Unknown command.\n\n" + HelpText(), true, nil
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

// webCommand hands the owner a link that opens the panel already signed in.
// A phone is the reason it exists: the panel's password is the one credential
// that cannot be pasted from the chat that needs it, and typing it on a phone
// keyboard is how an owner ends up not opening the panel at all.
func (s *CommandService) webCommand() (string, bool, error) {
	if s.PublicBaseURL == "" {
		return "The web panel address is unknown. Set `server.public_base_url` in config.yaml (or `EGGY_PUBLIC_BASE_URL`) and restart.", true, nil
	}
	if len(s.SigningKey) == 0 {
		return fmt.Sprintf("**Eggy web panel**\n\n%s\n\nSign in with the panel email and password: no signing key is configured, so I cannot send a one-tap link.", s.PublicBaseURL), true, nil
	}
	link := s.PublicBaseURL + "/auth/link?token=" + url.QueryEscape(session.SignLoginLink(s.SigningKey, s.Now().Add(webLoginLinkTTL)))
	return fmt.Sprintf("**Eggy web panel**\n\n%s\n\nOpening it signs you in for 12 hours. The link works once and expires in 5 minutes -- anyone who opens it first is in, so treat it like the password.", link), true, nil
}

func HelpText() string {
	return "Commands: /help, /status, /stop, /clear, /web, /model [alias], /mcp [add|remove|enable|disable|login|logout], /google [login|logout], /mode [strict|normal|auto], /restart"
}

func TelegramAutocomplete() []struct{ Name, Description string } {
	return append([]struct{ Name, Description string }(nil), telegramCommands...)
}
