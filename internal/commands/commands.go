package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
	mcpadapter "github.com/nigelteosw/eggy/plugins/tools/mcp"
)

// CommandService dispatches "/command" (Telegram) and "command --flag"
// (CLI) input through the shared catalog defined below. Handler
// implementations for each command family live in their own commands_*.go
// file, grouped by the domain they act on (repositories, skills, coding
// runs, schedules, model/thinking/usage settings, calendar, config, MCP);
// this file holds only the catalog registry and dispatch machinery shared
// by all of them.
type CommandService struct {
	config       config.Config
	store        ports.StateStore
	calendarAuth ports.CalendarAuthStore
	schedules    ScheduleLister
	context      ports.ContextStore
	conversation ConversationResetter
	changes      ChangeLister
	turns        TurnStopper
	repositories RepositoryAdmin
	skills       SkillAdmin
	agentRuntime AgentSettings
	channel      ports.Channel
	defaultModel string
	configPath   string
	modelAliases []string
	timezone     string
	now          func() time.Time
	restart      func()
	mcp          MCPCommands
	diagnostics  Diagnostics
}

// Options carries everything a CommandService needs. It exists so bootstrap
// can construct the service across the package boundary without the fields
// themselves becoming exported, mutable surface: every one of them is a
// collaborator wired once at startup, not a setting anything should change
// afterwards. Zero values are tolerated -- the tests build a service with
// only the few dependencies a given command touches, and handlers report an
// unavailable dependency rather than panicking on it.
type Options struct {
	Config       config.Config
	Store        ports.StateStore
	CalendarAuth ports.CalendarAuthStore
	Schedules    ScheduleLister
	Context      ports.ContextStore
	Conversation ConversationResetter
	Changes      ChangeLister
	Turns        TurnStopper
	Repositories RepositoryAdmin
	Skills       SkillAdmin
	AgentRuntime AgentSettings
	Channel      ports.Channel
	DefaultModel string
	ConfigPath   string
	ModelAliases []string
	Timezone     string
	Now          func() time.Time
	Restart      func()
	MCP          MCPCommands
	Diagnostics  Diagnostics
}

// New builds the command service from options.
func New(options Options) *CommandService {
	return &CommandService{
		config:       options.Config,
		store:        options.Store,
		calendarAuth: options.CalendarAuth,
		schedules:    options.Schedules,
		context:      options.Context,
		conversation: options.Conversation,
		changes:      options.Changes,
		turns:        options.Turns,
		repositories: options.Repositories,
		skills:       options.Skills,
		agentRuntime: options.AgentRuntime,
		channel:      options.Channel,
		defaultModel: options.DefaultModel,
		configPath:   options.ConfigPath,
		modelAliases: options.ModelAliases,
		timezone:     options.Timezone,
		now:          options.Now,
		restart:      options.Restart,
		mcp:          options.MCP,
		diagnostics:  options.Diagnostics,
	}
}

// The interfaces below are what this package needs from the kernel, declared
// here rather than in the kernel: each names the handful of methods the
// commands actually call, so the command surface cannot reach past them into
// the rest of a service. The narrowing is the point -- /clear resets one
// conversation and can never record a message, /runs reads changes and can
// never mutate one, /stop cancels a turn and can never begin or steer one.
// A bootstrap that passes the real *services.X still satisfies them
// structurally, so nothing in the kernel had to change to allow this.
//
// It is also why this package imports no kernel service package at all now,
// only ports and approvals.

// ScheduleLister reads the cron directory for the /schedules and /status
// commands. It is an interface so those commands stay testable without a
// scheduler.
type ScheduleLister interface {
	List(context.Context) ([]ports.Schedule, error)
}

// ConversationResetter clears one conversation's durable history for /clear.
// Recording and recall belong to the turn path, not to a command.
type ConversationResetter interface {
	Reset(ctx context.Context, conversationID string) error
}

// ChangeLister reads change history for /runs and /status. Every mutator on
// the underlying service is deliberately absent: a command reports on
// repository work, it never advances it.
type ChangeLister interface {
	List(ctx context.Context) ([]ports.Change, error)
}

// TurnStopper cancels the turn running in the calling conversation for
// /stop. Beginning, steering, and draining a turn belong to the orchestrator.
type TurnStopper interface {
	Stop(ctx context.Context) bool
}

// RepositoryAdmin is the /repositories surface. Adding requires owner
// approval and so returns an approval to deliver, while removing is a local
// bookkeeping change that does not.
type RepositoryAdmin interface {
	List(ctx context.Context) (map[string]ports.Repository, error)
	RequestAdd(ctx context.Context, name, cloneURL, baseBranch string, protectedBranches []string) (approvals.Approval, error)
	Remove(ctx context.Context, name string) error
}

// SkillAdmin is the /skills surface. Write and delete return an approval to
// deliver, since a skill's body steers later tool calls; disable and enable
// only change what is surfaced and take effect immediately.
type SkillAdmin interface {
	List(ctx context.Context) ([]ports.SkillSummary, error)
	Show(ctx context.Context, name string) (ports.Skill, error)
	RequestWrite(ctx context.Context, name, description, body string) (approvals.Approval, error)
	RequestDelete(ctx context.Context, name string) (approvals.Approval, error)
	Disable(ctx context.Context, name string) error
	Enable(ctx context.Context, name string) error
}

// AgentSettings is the owner-facing runtime settings surface: /model,
// /thinking, and /usage. RecordUsage is deliberately not here -- accumulating
// usage is the turn path's job, and a command only ever reads or resets it.
type AgentSettings interface {
	SelectedModel(ctx context.Context) (string, error)
	SelectModel(ctx context.Context, alias string) error
	ReasoningEffort(ctx context.Context) (string, error)
	ReasoningEfforts(alias string) []string
	SelectReasoningEffort(ctx context.Context, effort string) error
	ShowThinking(ctx context.Context) (bool, error)
	SetShowThinking(ctx context.Context, show bool) error
	Usage(ctx context.Context) (map[string]ports.ModelUsage, error)
	ResetUsage(ctx context.Context) error
}

// Diagnostics answers /capabilities and /context. Both reports are measured
// in the kernel, where the turn's own instruction assembly and tool filtering
// live, so a diagnostic cannot drift from what a turn actually sends.
type Diagnostics interface {
	CapabilityReport(ctx context.Context) (ports.CapabilityReport, error)
	ContextReport(ctx context.Context, conversationID string) (ports.ContextReport, error)
}

type MCPCommands interface {
	Statuses() []mcpadapter.ServerStatus
	Status(string) (mcpadapter.ServerStatus, error)
	Probe(context.Context, string) (mcpadapter.ProbeResult, error)
	BeginLogin(context.Context, string) (string, error)
	Logout(string) error
}

// Execute parses Telegram-style "/command key=value ..." input and
// dispatches it through the shared catalog, rendering the result as the
// small Markdown subset that Telegram's delivery path (channel.Deliver)
// converts to safe HTML. handled is false when input isn't a recognized
// command at all, so callers fall through to the model.
func (s *CommandService) Execute(ctx context.Context, input string) (string, bool, error) {
	req, ok := ParseTelegramInput(catalogIndex, input)
	if !ok {
		return "", false, nil
	}
	result, err := s.dispatch(ctx, req)
	return result.RenderMarkdown(), true, err
}

// ExecuteCLI parses conventional "command --flag=value ..." CLI arguments
// (already shell-split, without the program name or global flags) through
// the same catalog, returning the structured result so the caller can render
// clean plain text.
func (s *CommandService) ExecuteCLI(ctx context.Context, args []string) (CommandResult, bool, error) {
	req, ok := ParseCLIArgs(catalogIndex, args)
	if !ok {
		return CommandResult{}, false, nil
	}
	result, err := s.dispatch(ctx, req)
	return result, true, err
}

// Dispatch runs an already-parsed request through the catalog. It is the
// entry point for callers that build a CommandRequest themselves rather than
// parsing a Telegram or CLI string -- the web API, which gets its arguments
// as JSON. An unknown path yields a zero CommandResult and a nil error, the
// same "not handled" signal Execute translates into its handled=false.
func (s *CommandService) Dispatch(ctx context.Context, req CommandRequest) (CommandResult, error) {
	return s.dispatch(ctx, req)
}

// SetMCP attaches the MCP manager after construction, which bootstrap needs
// because the manager is built after the command service it belongs to.
// Callers must not call this with a nil *mcpadapter.Manager: that stores a
// non-nil MCPCommands wrapping a nil pointer, so every handler's
// "is MCP configured" guard passes and then calls a method on a nil receiver
// instead of reporting that MCP is not configured.
func (s *CommandService) SetMCP(mcp MCPCommands) { s.mcp = mcp }

func (s *CommandService) dispatch(ctx context.Context, req CommandRequest) (CommandResult, error) {
	entry, ok := catalogIndex[strings.Join(req.Path, " ")]
	if !ok {
		return CommandResult{}, nil
	}
	return entry.Handler(ctx, s, req)
}

// ExecuteConfigCLI dispatches a "config ..." CLI command directly against
// configPath. It shares the exact catalog, parser, and handlers the
// Telegram and full-CLI paths use, but never constructs the full App
// runtime: no Telegram client, model provider, or provider credentials are
// required just to read or write config.yaml.
func ExecuteConfigCLI(ctx context.Context, configPath string, args []string) (CommandResult, bool, error) {
	return (&CommandService{configPath: configPath}).ExecuteCLI(ctx, args)
}

// topLevelCommandOrder is the stable display order for the bare /help
// listing, eggy help, and Telegram's autocomplete list.
var topLevelCommandOrder = []string{
	"start", "help", "status", "capabilities", "context", "repositories", "runs", "stop",
	"schedules", "memory", "skills", "model", "thinking", "config", "usage", "clear",
	"calendar_auth", "mcp", "restart",
}

// HelpText renders help for a specific command, or a list of every top-level
// command with its one-line summary when command is empty. It needs no
// CommandService: help is pure catalog metadata.
func HelpText(command string) string {
	command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if command != "" {
		entry, ok := catalogIndex[command]
		if !ok {
			return fmt.Sprintf("Unknown command %q. Type /help for a list of commands.", command)
		}
		lines := []string{entry.Summary}
		if entry.Detail != "" {
			lines = append(lines, entry.Detail)
		}
		for _, example := range entry.Examples {
			lines = append(lines, "Telegram: "+example.Telegram, "CLI: "+example.CLI)
		}
		return strings.Join(lines, "\n")
	}
	lines := make([]string, 0, len(topLevelCommandOrder))
	for _, path := range topLevelCommandOrder {
		entry, ok := catalogIndex[path]
		if !ok {
			continue
		}
		lines = append(lines, "/"+entry.Path+" — "+entry.Summary)
	}
	return strings.Join(lines, "\n")
}

// TelegramAutocomplete returns the top-level commands, in stable order, for
// registering Telegram's autocomplete list. It is the single source that
// list is generated from, so it cannot drift from /help or eggy help.
func TelegramAutocomplete() []struct{ Name, Description string } {
	result := make([]struct{ Name, Description string }, 0, len(topLevelCommandOrder))
	for _, path := range topLevelCommandOrder {
		entry, ok := catalogIndex[path]
		if !ok {
			continue
		}
		result = append(result, struct{ Name, Description string }{Name: entry.Path, Description: entry.Summary})
	}
	return result
}

func mustEntry(path string) CatalogEntry { return catalogIndex[path] }

// usageHelp formats a malformed command as actionable help: the missing or
// invalid field, plus a canonical Telegram example and CLI example, instead
// of a bare usage line.
func usageHelp(entry CatalogEntry, problem string) CommandResult {
	result := CommandResult{State: ResultHelp, Title: "Usage: /" + entry.Path, Detail: problem}
	if len(entry.Examples) > 0 {
		result.Fields = []ResultField{
			{Label: "Telegram", Value: entry.Examples[0].Telegram},
			{Label: "CLI", Value: entry.Examples[0].CLI},
		}
	}
	return result
}

func errorResult(err error) CommandResult {
	return CommandResult{State: ResultError, Title: err.Error()}
}

// catalog and catalogIndex are populated in init(), not as plain var
// initializers: every handler transitively reaches catalogIndex (through
// usageHelp/mustEntry/HelpText), and Go's package-init dependency analysis
// treats storing those handler functions in the catalog literal as a
// reference to everything they touch, which would otherwise be flagged as an
// initialization cycle even though nothing is actually evaluated until a
// command is dispatched.
var catalog []CatalogEntry
var catalogIndex map[string]CatalogEntry

// Catalog returns every registered command, in registration order. It hands
// back a copy so a caller enumerating the catalog -- the help text, the
// Telegram command list, and the coverage test that dispatches every entry's
// own example -- cannot reorder or truncate the live one.
func Catalog() []CatalogEntry { return slices.Clone(catalog) }

func init() {
	catalog = []CatalogEntry{
		{
			Path:    "status",
			Summary: "Show operational status: repositories, active runs, pending approvals, schedules, and active model",
			Examples: []Example{
				{Telegram: "/status", CLI: "eggy status"},
			},
			Handler: handleStatus,
		},
		{
			Path:    "start",
			Summary: "Show the welcome message",
			Examples: []Example{
				{Telegram: "/start", CLI: "eggy start"},
			},
			Handler: handleStart,
		},
		{
			Path:    "help",
			Summary: "Show available commands, or usage for one command",
			Examples: []Example{
				{Telegram: "/help repositories", CLI: "eggy help repositories"},
			},
			Handler: handleHelp,
		},
		{
			Path:    "repositories",
			Summary: "List configured repositories, add one, or remove one",
			Examples: []Example{
				{Telegram: "/repositories", CLI: "eggy repositories"},
			},
			Handler: handleRepositories,
		},
		{
			Path:    "repositories add",
			Summary: "Request adding a repository (requires owner approval)",
			Examples: []Example{
				{Telegram: "/repositories add eggy https://github.com/nigelteosw/eggy.git main", CLI: "eggy repositories add eggy https://github.com/nigelteosw/eggy.git main"},
			},
			Handler: handleRepositoriesAdd,
		},
		{
			Path:    "repositories remove",
			Summary: "Remove a configured repository",
			Examples: []Example{
				{Telegram: "/repositories remove eggy", CLI: "eggy repositories remove eggy"},
			},
			Handler: handleRepositoriesRemove,
		},
		{
			Path:    "skills",
			Summary: "List installed procedural skills",
			Examples: []Example{
				{Telegram: "/skills", CLI: "eggy skills"},
			},
			Handler: handleSkills,
		},
		{
			Path:    "skills show",
			Summary: "Show one skill's full description and content",
			Examples: []Example{
				{Telegram: "/skills show fix-flaky-tests", CLI: "eggy skills show fix-flaky-tests"},
			},
			Handler: handleSkillsShow,
		},
		{
			Path:    "skills add",
			Summary: "Propose a new skill (requires owner approval)",
			Examples: []Example{
				{Telegram: "/skills add fix-flaky-tests\nUse when a test intermittently fails on timing\n\n1. Rerun with -count=10\n2. Look for shared state", CLI: "eggy skills add fix-flaky-tests \"Use when a test intermittently fails on timing\n\n1. Rerun with -count=10\n2. Look for shared state\""},
			},
			Handler: handleSkillsAdd,
		},
		{
			Path:    "skills edit",
			Summary: "Propose replacing an existing skill's description and content (requires owner approval)",
			Examples: []Example{
				{Telegram: "/skills edit fix-flaky-tests\nUpdated description\n\nUpdated steps", CLI: "eggy skills edit fix-flaky-tests \"Updated description\n\nUpdated steps\""},
			},
			Handler: handleSkillsEdit,
		},
		{
			Path:    "skills remove",
			Summary: "Propose deleting a skill (requires owner approval)",
			Examples: []Example{
				{Telegram: "/skills remove fix-flaky-tests", CLI: "eggy skills remove fix-flaky-tests"},
			},
			Handler: handleSkillsRemove,
		},
		{
			Path:    "skills disable",
			Summary: "Disable a skill so the agent stops seeing it (no approval needed; reversible)",
			Examples: []Example{
				{Telegram: "/skills disable fix-flaky-tests", CLI: "eggy skills disable fix-flaky-tests"},
			},
			Handler: handleSkillsDisable,
		},
		{
			Path:    "skills enable",
			Summary: "Re-enable a previously disabled skill",
			Examples: []Example{
				{Telegram: "/skills enable fix-flaky-tests", CLI: "eggy skills enable fix-flaky-tests"},
			},
			Handler: handleSkillsEnable,
		},
		{
			Path:    "runs",
			Summary: "List coding-agent runs",
			Examples: []Example{
				{Telegram: "/runs", CLI: "eggy runs"},
			},
			Handler: handleRuns,
		},
		{
			Path:    "runs show",
			Summary: "Show one run in full: branch, base revision, phase, validation, elapsed time, and pull request",
			Examples: []Example{
				{Telegram: "/runs show run-1", CLI: "eggy runs show run-1"},
			},
			Handler: handleRunsShow,
		},
		{
			Path:    "capabilities",
			Summary: "Show the active model, registered tools, repositories, integrations, and shipping readiness",
			Examples: []Example{
				{Telegram: "/capabilities", CLI: "eggy capabilities"},
			},
			Handler: handleCapabilities,
		},
		{
			Path:    "context",
			Summary: "Show what is resident in this conversation's context, and the limits that compact or truncate it",
			Examples: []Example{
				{Telegram: "/context", CLI: "eggy context"},
			},
			Handler: handleContext,
		},
		{
			Path:    "stop",
			Summary: "Stop the turn currently running in this conversation",
			Examples: []Example{
				{Telegram: "/stop", CLI: "eggy stop"},
			},
			Handler: handleStop,
		},
		{
			Path:    "schedules",
			Summary: "List scheduled instructions",
			Examples: []Example{
				{Telegram: "/schedules", CLI: "eggy schedules"},
			},
			Handler: handleSchedules,
		},
		{
			Path:    "memory",
			Summary: "Show durable memory (USER.md / MEMORY.md)",
			Examples: []Example{
				{Telegram: "/memory", CLI: "eggy memory"},
			},
			Handler: handleMemory,
		},
		{
			Path:    "clear",
			Summary: "Clear the disposable recent-conversation window (durable memory is unchanged)",
			Examples: []Example{
				{Telegram: "/clear", CLI: "eggy clear"},
			},
			Handler: handleClear,
		},
		{
			Path:    "model",
			Summary: "Show the active model, or switch to a configured alias",
			Examples: []Example{
				{Telegram: "/model deepseek-pro", CLI: "eggy model deepseek-pro"},
			},
			Handler: handleModel,
		},
		{
			Path:    "model effort",
			Summary: "Set the active model's reasoning effort",
			Examples: []Example{
				{Telegram: "/model effort high", CLI: "eggy model effort high"},
			},
			Handler: handleModelEffort,
		},
		{
			Path:    "model default",
			Summary: "Reset the active model to the configured default",
			Examples: []Example{
				{Telegram: "/model default", CLI: "eggy model default"},
			},
			Handler: handleModelDefault,
		},
		{
			Path:    "thinking",
			Summary: "Show whether the model's raw reasoning is delivered as a separate message",
			Examples: []Example{
				{Telegram: "/thinking", CLI: "eggy thinking"},
			},
			Handler: handleThinking,
		},
		{
			Path:    "thinking show",
			Summary: "Deliver the model's raw reasoning as a separate \"Thinking:\" message",
			Examples: []Example{
				{Telegram: "/thinking show", CLI: "eggy thinking show"},
			},
			Handler: handleThinkingShow,
		},
		{
			Path:    "thinking hide",
			Summary: "Stop delivering the model's raw reasoning as a separate message",
			Examples: []Example{
				{Telegram: "/thinking hide", CLI: "eggy thinking hide"},
			},
			Handler: handleThinkingHide,
		},
		{
			Path:    "config",
			Summary: "Get or set configuration sections",
			Examples: []Example{
				{Telegram: "/config get providers", CLI: "eggy config get providers"},
			},
			Handler: handleConfigUsage,
		},
		{
			Path:    "config get",
			Summary: "Get a configuration section",
			Examples: []Example{
				{Telegram: "/config get providers", CLI: "eggy config get providers"},
			},
			Handler: handleConfigGetUsage,
		},
		{
			Path:    "config get providers",
			Summary: "Show configured model providers",
			Examples: []Example{
				{Telegram: "/config get providers", CLI: "eggy config get providers"},
			},
			Handler: handleConfigGetProviders,
		},
		{
			Path:    "config get models",
			Summary: "Show configured model aliases",
			Examples: []Example{
				{Telegram: "/config get models", CLI: "eggy config get models"},
			},
			Handler: handleConfigGetModels,
		},
		{
			Path:    "config get calendar",
			Summary: "Show Calendar configuration",
			Examples: []Example{
				{Telegram: "/config get calendar", CLI: "eggy config get calendar"},
			},
			Handler: handleConfigGetCalendar,
		},
		{
			Path:    "config get path",
			Summary: "Show the config.yaml file path",
			Examples: []Example{
				{Telegram: "/config get path", CLI: "eggy config get path"},
			},
			Handler: handleConfigGetPath,
		},
		{
			Path:    "config set",
			Summary: "Set a configuration section",
			Examples: []Example{
				{Telegram: "/config set model alias=deepseek-pro provider=deepseek model=deepseek-v4-pro reasoning_efforts=low,medium,high,max", CLI: "eggy config set model --alias=deepseek-pro --provider=deepseek --model=deepseek-v4-pro --reasoning-efforts=low,medium,high,max"},
			},
			Handler: handleConfigSetUsage,
		},
		{
			Path:    "config set provider",
			Summary: "Add or update a model provider",
			Examples: []Example{
				{Telegram: "/config set provider name=openrouter adapter=openai_compatible base_url=https://openrouter.ai/api/v1 api_key_env=OPENROUTER_API_KEY", CLI: "eggy config set provider --name=openrouter --adapter=openai_compatible --base-url=https://openrouter.ai/api/v1 --api-key-env=OPENROUTER_API_KEY"},
			},
			Handler: handleConfigSetProvider,
		},
		{
			Path:    "config set model",
			Summary: "Add or update a model alias",
			Examples: []Example{
				{Telegram: "/config set model alias=deepseek-pro provider=deepseek model=deepseek-v4-pro reasoning_efforts=low,medium,high,max", CLI: "eggy config set model --alias=deepseek-pro --provider=deepseek --model=deepseek-v4-pro --reasoning-efforts=low,medium,high,max"},
			},
			Handler: handleConfigSetModel,
		},
		{
			Path:    "config set calendar",
			Summary: "Update Calendar configuration",
			Examples: []Example{
				{Telegram: "/config set calendar timezone=Asia/Singapore", CLI: "eggy config set calendar --timezone=Asia/Singapore"},
			},
			Handler: handleConfigSetCalendar,
		},
		{
			Path:    "config show",
			Summary: "Show the whole config.yaml as YAML (safe to show in full: it never holds secret values, only environment-variable names)",
			Examples: []Example{
				{Telegram: "/config show", CLI: "eggy config show"},
			},
			Handler: handleConfigShow,
		},
		{
			Path:    "usage",
			Summary: "Show local token usage counters, or reset them",
			Examples: []Example{
				{Telegram: "/usage", CLI: "eggy usage"},
			},
			Handler: handleUsage,
		},
		{
			Path:    "usage reset",
			Summary: "Reset local token usage counters (provider billing is unaffected)",
			Examples: []Example{
				{Telegram: "/usage reset", CLI: "eggy usage reset"},
			},
			Handler: handleUsageReset,
		},
		{
			Path:    "calendar_auth",
			Summary: "Start Google Calendar enrollment (single-use link, expires in 10 minutes)",
			Examples: []Example{
				{Telegram: "/calendar_auth", CLI: "eggy calendar_auth"},
			},
			Handler: handleCalendarAuth,
		},
		{
			Path:    "restart",
			Summary: "Restart Eggy to pick up config changes",
			Examples: []Example{
				{Telegram: "/restart", CLI: "eggy restart"},
			},
			Handler: handleRestart,
		},
		{
			Path: "mcp", Summary: "List and manage configured MCP servers",
			Detail:   mcpExplanation,
			Examples: []Example{{Telegram: "/mcp", CLI: "eggy mcp"}}, Handler: handleMCP,
		},
		{
			Path: "mcp status", Summary: "Show one MCP server's status",
			Examples: []Example{{Telegram: "/mcp status railway", CLI: "eggy mcp status railway"}}, Handler: handleMCPStatus,
		},
		{
			Path: "mcp probe", Summary: "Probe one MCP server's tool catalog",
			Examples: []Example{{Telegram: "/mcp probe railway", CLI: "eggy mcp probe railway"}}, Handler: handleMCPProbe,
		},
		{
			Path: "mcp login", Summary: "Start OAuth login for one MCP server",
			Examples: []Example{{Telegram: "/mcp login railway", CLI: "eggy mcp login railway"}}, Handler: handleMCPLogin,
		},
		{
			Path: "mcp logout", Summary: "Remove one MCP server's OAuth credentials",
			Examples: []Example{{Telegram: "/mcp logout railway", CLI: "eggy mcp logout railway"}}, Handler: handleMCPLogout,
		},
		{
			Path: "mcp reload", Summary: "Restart Eggy to reload an MCP catalog",
			Examples: []Example{{Telegram: "/mcp reload railway", CLI: "eggy mcp reload railway"}}, Handler: handleMCPReload,
		},
	}
	catalogIndex = buildCatalogIndex(catalog)
}
