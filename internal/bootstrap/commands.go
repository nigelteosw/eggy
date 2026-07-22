package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type CommandService struct {
	config       Config
	store        ports.StateStore
	context      ports.ContextStore
	conversation *services.ConversationService
	coding       *services.CodingService
	shipping     *services.ShippingService
	repositories *services.RepositoriesService
	agentRuntime *services.AgentRuntime
	channel      ports.Channel
	owner        string
	defaultModel string
	configPath   string
	modelAliases []string
	timezone     string
	now          func() time.Time
	restart      func()
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
	"start", "help", "status", "repositories", "runs", "continue", "stop",
	"schedules", "memory", "model", "config", "usage", "clear",
	"calendar_auth", "restart",
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
			Path:    "runs",
			Summary: "List coding-agent runs",
			Examples: []Example{
				{Telegram: "/runs", CLI: "eggy runs"},
			},
			Handler: handleRuns,
		},
		{
			Path:    "continue",
			Summary: "Resume the latest (or a specific) coding session",
			Examples: []Example{
				{Telegram: "/continue run-1 add the missing test", CLI: "eggy continue run-1 add the missing test"},
			},
			Handler: handleContinue,
		},
		{
			Path:    "stop",
			Summary: "Stop a running coding-agent run",
			Examples: []Example{
				{Telegram: "/stop run-1", CLI: "eggy stop run-1"},
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
	}
	catalogIndex = buildCatalogIndex(catalog)
}

func handleStatus(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	sort.Strings(names)
	repositories := "none"
	if len(names) > 0 {
		repositories = strings.Join(names, ", ")
	}
	pending := 0
	for _, approval := range state.Approvals {
		if approval.Status == approvals.Pending {
			pending++
		}
	}
	active := 0
	if s.coding != nil {
		sessions, err := s.coding.List(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		for _, session := range sessions {
			if session.Phase == ports.PhaseRunning {
				active++
			}
		}
	}
	model := "unconfigured"
	if s.agentRuntime != nil {
		if selected, err := s.agentRuntime.SelectedModel(ctx); err == nil && selected != "" {
			model = selected
		}
	}
	var next []string
	if len(names) == 0 {
		next = append(next, "/repositories add <name> <clone_url> [base_branch] [protected_branches]")
	}
	if pending > 0 {
		next = append(next, "/repositories")
	}
	if active > 0 {
		next = append(next, "/runs")
	}
	return CommandResult{
		Title: "Eggy status",
		Fields: []ResultField{
			{Label: "Repositories", Value: repositories},
			{Label: "Active runs", Value: fmt.Sprintf("%d", active)},
			{Label: "Pending approvals", Value: fmt.Sprintf("%d", pending)},
			{Label: "Schedules", Value: fmt.Sprintf("%d", len(state.Schedules))},
			{Label: "Active model", Value: model},
		},
		Next: next,
	}, nil
}

func handleStart(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	return CommandResult{
		Title:  "Hey, I'm Eggy",
		Detail: "Your personal AI assistant! I can chat, manage code repositories, schedule reminders, and more.\n\n" + HelpText("") + "\n\nType /help <command> for detailed usage on any command.",
	}, nil
}

func handleHelp(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	command := ""
	if len(req.Args) > 0 {
		command = req.Args[0]
	}
	return CommandResult{Detail: HelpText(command)}, nil
}

type repositoryAddPayload struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

// pendingRepositoryAddNames returns the names of add-repository approvals
// still pending owner approval, excluding any that already made it into
// state (approved between load and now).
func pendingRepositoryAddNames(state ports.State, registered map[string]ports.Repository) []string {
	var names []string
	for _, approval := range state.Approvals {
		if approval.Status != approvals.Pending || approval.Action != approvals.AddRepository {
			continue
		}
		var payload repositoryAddPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil || payload.Name == "" {
			continue
		}
		if _, exists := registered[payload.Name]; exists {
			continue
		}
		names = append(names, payload.Name)
	}
	sort.Strings(names)
	return names
}

func handleRepositories(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("repositories"), fmt.Sprintf("Unknown repositories subcommand %q. Use add or remove.", req.Args[0])), nil
	}
	registered, err := s.repositories.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	pendingNames := pendingRepositoryAddNames(state, registered)
	if len(registered) == 0 && len(pendingNames) == 0 {
		return CommandResult{
			State: ResultInfo,
			Title: "No repositories configured.",
			Next:  []string{"/repositories add <name> <clone_url> [base_branch] [protected_branches]"},
		}, nil
	}
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		repository := registered[name]
		rows = append(rows, []string{name, repository.BaseBranch, strings.Join(repository.ProtectedBranches, ", ")})
	}
	var lines []string
	for _, name := range pendingNames {
		lines = append(lines, name+" — awaiting owner approval")
	}
	return CommandResult{
		TableHeaders: []string{"Repository", "Base branch", "Protected branches"},
		TableRows:    rows,
		Lines:        lines,
		Next:         []string{"/repositories add <name> <clone_url> [base_branch] [protected_branches]", "/repositories remove <name>"},
	}, nil
}

func handleRepositoriesAdd(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) < 2 || len(req.Args) > 4 {
		return usageHelp(mustEntry("repositories add"), "Expected <name> <clone_url>, with optional [base_branch] and [protected_branches] (comma-separated)."), nil
	}
	name, cloneURL := req.Args[0], req.Args[1]
	baseBranch := ""
	if len(req.Args) >= 3 {
		baseBranch = req.Args[2]
	}
	var protectedBranches []string
	if len(req.Args) == 4 {
		for _, branch := range strings.Split(req.Args[3], ",") {
			if trimmed := strings.TrimSpace(branch); trimmed != "" {
				protectedBranches = append(protectedBranches, trimmed)
			}
		}
	}
	approval, err := s.repositories.RequestAdd(ctx, name, cloneURL, baseBranch, protectedBranches)
	if err != nil {
		return errorResult(err), nil
	}
	if s.channel != nil {
		if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
			return CommandResult{}, err
		}
	}
	return CommandResult{
		State:  ResultInfo,
		Title:  "Add request for " + name + " staged, awaiting approval.",
		Detail: "The owner will see an Approve/Reject prompt.",
		Next:   []string{"/repositories"},
	}, nil
}

func handleRepositoriesRemove(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.repositories == nil {
		return CommandResult{State: ResultInfo, Title: "Repositories are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("repositories remove"), "Expected exactly one <name>."), nil
	}
	if err := s.repositories.Remove(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Removed " + req.Args[0] + ".", Next: []string{"/repositories"}}, nil
}

func handleRuns(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	sessions, err := s.coding.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(sessions) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No coding runs.",
			Detail: "An implementation run starts when you ask Eggy to change a configured repository, or with /continue.",
		}, nil
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	rows := make([][]string, 0, len(sessions))
	for _, session := range sessions {
		validation := session.Validation
		if validation == "" {
			validation = "—"
		}
		rows = append(rows, []string{session.ID, session.Repository, string(session.Phase), validation})
	}
	return CommandResult{
		TableHeaders: []string{"Run", "Repository", "Status", "Validation"},
		TableRows:    rows,
		Next:         []string{"/continue <run-id>", "/stop <run-id>"},
	}, nil
}

func handleContinue(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil || s.shipping == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	var runID, instruction string
	if len(req.Args) > 0 {
		runID = req.Args[0]
		instruction = strings.TrimSpace(strings.TrimPrefix(req.Tail, runID))
	}
	if instruction == "" {
		instruction = "Continue the approved task, inspect the current state, and complete the next safe implementation step."
	}
	var run ports.ImplementationSession
	var result ports.CodingResult
	var err error
	if runID == "" {
		run, result, err = s.coding.ResumeLatest(ctx, instruction, nil)
	} else {
		run, result, err = s.coding.Resume(ctx, runID, instruction, nil)
	}
	if err != nil {
		return errorResult(err), nil
	}
	pr, note, err := s.shipping.Ship(ctx, run.ID, run.Branch, result.CommitMessage)
	if err != nil {
		return errorResult(err), nil
	}
	if note != "" {
		return CommandResult{
			Title:  "Implementation session " + run.ID,
			Detail: note,
		}, nil
	}
	_ = s.coding.Cleanup(ctx, run.ID)
	return CommandResult{
		Title:  "Implementation session " + run.ID + " shipped",
		Fields: []ResultField{{Label: "Pull request", Value: pr.URL}},
	}, nil
}

func handleStop(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.coding == nil {
		return CommandResult{State: ResultInfo, Title: "Coding is not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("stop"), "Expected exactly one <run-id>."), nil
	}
	if err := s.coding.Stop(req.Args[0]); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Stop requested for " + req.Args[0] + ".", Next: []string{"/runs"}}, nil
}

func handleSchedules(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(state.Schedules) == 0 {
		return CommandResult{
			State:  ResultInfo,
			Title:  "No schedules.",
			Detail: "Ask Eggy in conversation to schedule an instruction, e.g. \"remind me every morning at 9am to check email.\"",
		}, nil
	}
	ids := make([]string, 0, len(state.Schedules))
	for id := range state.Schedules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		schedule := state.Schedules[id]
		enabled := "yes"
		if !schedule.Enabled {
			enabled = "no"
		}
		nextRun := "—"
		if !schedule.NextRun.IsZero() {
			nextRun = schedule.NextRun.Format("2006-01-02 15:04 MST")
		}
		rows = append(rows, []string{schedule.Instruction, nextRun, enabled})
	}
	timezone := s.timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return CommandResult{
		TableHeaders: []string{"Instruction", "Next run", "Enabled"},
		TableRows:    rows,
		Fields:       []ResultField{{Label: "Owner timezone", Value: timezone}},
	}, nil
}

func handleMemory(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.context == nil {
		return CommandResult{State: ResultInfo, Title: "Memory is not configured."}, nil
	}
	loaded, err := s.context.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	detail := "Durable memory (USER.md / MEMORY.md) persists across restarts and context resets; /clear does not touch it."
	if strings.TrimSpace(loaded.Memory) == "" {
		detail += "\n\nNo durable memory yet."
	} else {
		detail += "\n\n" + loaded.Memory
	}
	return CommandResult{Title: "Durable memory", Detail: detail}, nil
}

func handleClear(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if err := s.conversation.Reset(ctx); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{
		Title:  "Cleared the disposable recent-conversation window.",
		Detail: "Durable memory (USER.md / MEMORY.md) is unchanged.",
	}, nil
}

func sortedModelAliases(s *CommandService) []string {
	aliases := append([]string(nil), s.modelAliases...)
	sort.Strings(aliases)
	return aliases
}

func handleModel(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if len(req.Args) == 0 {
		activeAlias, err := s.agentRuntime.SelectedModel(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		effort, err := s.agentRuntime.ReasoningEffort(ctx)
		if err != nil {
			return CommandResult{}, err
		}
		aliases := sortedModelAliases(s)
		rows := make([][]string, 0, len(aliases))
		for _, alias := range aliases {
			efforts := "—"
			if allowed := s.agentRuntime.ReasoningEfforts(alias); len(allowed) > 0 {
				efforts = strings.Join(allowed, ", ")
			}
			isActive := ""
			if alias == activeAlias {
				isActive = "yes"
			}
			rows = append(rows, []string{alias, efforts, isActive})
		}
		effortValue := effort
		if effortValue == "" {
			effortValue = "—"
		}
		return CommandResult{
			Fields:       []ResultField{{Label: "Active model", Value: activeAlias}, {Label: "Reasoning effort", Value: effortValue}},
			TableHeaders: []string{"Alias", "Allowed reasoning efforts", "Active"},
			TableRows:    rows,
			Next:         []string{"/model <alias>", "/model effort <level>", "/model default"},
		}, nil
	}
	alias := req.Args[0]
	if err := s.agentRuntime.SelectModel(ctx, alias); err != nil {
		return CommandResult{State: ResultError, Title: err.Error(), Detail: "Configured models: " + strings.Join(sortedModelAliases(s), ", ")}, nil
	}
	return CommandResult{Title: "Model set to " + alias + "."}, nil
}

func handleModelEffort(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("model effort"), "Expected exactly one level: low, medium, high, or max."), nil
	}
	if err := s.agentRuntime.SelectReasoningEffort(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Reasoning effort set to " + req.Args[0] + "."}, nil
}

func handleModelDefault(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Model selection is not configured."}, nil
	}
	if err := s.agentRuntime.SelectModel(ctx, ""); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Model reset to " + s.defaultModel + "."}, nil
}

func handleUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Usage tracking is not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("usage"), fmt.Sprintf("Unknown usage subcommand %q.", req.Args[0])), nil
	}
	usage, err := s.agentRuntime.Usage(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	aliases := sortedModelAliases(s)
	rows := make([][]string, 0, len(aliases))
	for _, alias := range aliases {
		value := usage[alias]
		rows = append(rows, []string{
			alias,
			fmt.Sprintf("%d", value.PromptTokens),
			fmt.Sprintf("%d", value.CompletionTokens),
			fmt.Sprintf("%d", value.CachedPromptTokens),
			fmt.Sprintf("%d", value.ReasoningTokens),
			fmt.Sprintf("%d", value.TotalTokens),
		})
	}
	return CommandResult{
		TableHeaders: []string{"Model", "Prompt", "Completion", "Cached", "Reasoning", "Total"},
		TableRows:    rows,
		Detail:       "Local totals are provider-reported and do not replace the provider billing dashboard.",
		Next:         []string{"/usage reset"},
	}, nil
}

func handleUsageReset(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.agentRuntime == nil {
		return CommandResult{State: ResultInfo, Title: "Usage tracking is not configured."}, nil
	}
	if err := s.agentRuntime.ResetUsage(ctx); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Title: "Local provider-reported usage counters reset.", Detail: "Provider billing records are unchanged."}, nil
}

func handleCalendarAuth(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if !s.config.Calendar.Enabled {
		return CommandResult{State: ResultInfo, Title: "Calendar is not configured."}, nil
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return CommandResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.Calendar.EnrollmentDigest = hex.EncodeToString(digest[:])
		state.Calendar.EnrollmentExpires = now().Add(10 * time.Minute)
		return nil
	})
	if err != nil {
		return CommandResult{}, err
	}
	link := s.config.Server.PublicBaseURL + "/auth/google?enrollment=" + url.QueryEscape(token)
	return CommandResult{
		Title:  "Google Calendar enrollment started.",
		Detail: "This link authorizes Eggy to read and write your Google Calendar. It is single-use and expires in 10 minutes.",
		Fields: []ResultField{{Label: "Enrollment link", Value: link}},
	}, nil
}

func handleRestart(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.restart == nil {
		return CommandResult{State: ResultInfo, Title: "Restart is not available in this environment."}, nil
	}
	s.restart()
	return CommandResult{
		Title:  "Restarting Eggy to pick up config changes. Back in a few seconds.",
		Detail: "Any active implementation session is interrupted safely and can be resumed with /continue once Eggy is back.",
	}, nil
}

func handleConfigUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	return usageHelp(mustEntry("config"), fmt.Sprintf("Unknown config subcommand %q. Use get, set, or show.", firstOrEmpty(req.Args))), nil
}

func handleConfigGetUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	problem := "Expected exactly one section: providers, models, calendar, or path."
	if len(req.Args) > 0 {
		problem = fmt.Sprintf("Unknown config section %q. Use providers, models, calendar, or path.", req.Args[0])
	}
	return usageHelp(mustEntry("config get"), problem), nil
}

func handleConfigSetUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	problem := "Expected exactly one section: provider, model, or calendar."
	if len(req.Args) > 0 {
		problem = fmt.Sprintf("Unknown config section %q. Use provider, model, or calendar.", req.Args[0])
	}
	return usageHelp(mustEntry("config set"), problem), nil
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func handleConfigGetProviders(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return CommandResult{State: ResultInfo, Title: "No providers configured.", Next: []string{"/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"}}, nil
	}
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		rows = append(rows, []string{name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv})
	}
	return CommandResult{
		TableHeaders: []string{"Provider", "Adapter", "Base URL", "API key env"},
		TableRows:    rows,
		Detail:       "api_key_env names the environment variable holding the key; it is a reference, not the secret value itself.",
	}, nil
}

func handleConfigGetModels(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		return CommandResult{State: ResultInfo, Title: "No models configured.", Next: []string{"/config set model alias=<alias> provider=<provider> model=<model_id>"}}, nil
	}
	rows := make([][]string, 0, len(aliases))
	for _, alias := range aliases {
		model := cfg.ModelAliases[alias]
		efforts := "—"
		if len(model.ReasoningEfforts) > 0 {
			efforts = strings.Join(model.ReasoningEfforts, ", ")
		}
		rows = append(rows, []string{alias, model.Provider, model.Model, efforts})
	}
	return CommandResult{TableHeaders: []string{"Alias", "Provider", "Model", "Reasoning efforts"}, TableRows: rows}, nil
}

func handleConfigGetCalendar(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Fields: []ResultField{
		{Label: "Enabled", Value: fmt.Sprintf("%t", cfg.Calendar.Enabled)},
		{Label: "Default calendar", Value: cfg.Calendar.DefaultCalendar},
		{Label: "Timezone", Value: cfg.Calendar.Timezone},
	}}, nil
}

func handleConfigGetPath(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	return CommandResult{Title: s.configPath}, nil
}

func handleConfigShow(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	text, err := ShowConfigText(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Detail: text}, nil
}

func handleConfigSetProvider(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	name, adapter, baseURL, apiKeyEnv := req.Named["name"], req.Named["adapter"], req.Named["base_url"], req.Named["api_key_env"]
	if name == "" || adapter == "" || baseURL == "" || apiKeyEnv == "" {
		return usageHelp(mustEntry("config set provider"), "Required: name, adapter, base_url, api_key_env."), nil
	}
	if err := SetProvider(s.configPath, name, adapter, baseURL, apiKeyEnv); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set provider " + name + ".",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}

func handleConfigSetModel(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	alias, provider, modelID, reasoningEfforts := req.Named["alias"], req.Named["provider"], req.Named["model"], req.Named["reasoning_efforts"]
	if alias == "" || provider == "" || modelID == "" {
		return usageHelp(mustEntry("config set model"), "Required: alias, provider, model. Optional: reasoning_efforts (comma-separated)."), nil
	}
	if err := SetModelAlias(s.configPath, alias, provider, modelID, reasoningEfforts); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set model " + alias + ".",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}

func handleConfigSetCalendar(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	enabled, defaultCalendar, timezone := req.Named["enabled"], req.Named["default_calendar"], req.Named["timezone"]
	for key := range req.Named {
		if key != "enabled" && key != "default_calendar" && key != "timezone" {
			return usageHelp(mustEntry("config set calendar"), fmt.Sprintf("Unknown field %q. Use enabled, default_calendar, or timezone.", key)), nil
		}
	}
	if enabled == "" && defaultCalendar == "" && timezone == "" {
		return usageHelp(mustEntry("config set calendar"), "At least one of enabled, default_calendar, or timezone is required."), nil
	}
	if err := SetCalendar(s.configPath, enabled, defaultCalendar, timezone); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set calendar.",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}
