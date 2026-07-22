package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/calendar/google"
	"github.com/nigelteosw/eggy/internal/adapters/channels/telegram"
	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/models/openaicompat"
	githubadapter "github.com/nigelteosw/eggy/internal/adapters/repositories/github"
	"github.com/nigelteosw/eggy/internal/adapters/runner/localprocess"
	schedulerlocal "github.com/nigelteosw/eggy/internal/adapters/scheduler/local"
	sessionjson "github.com/nigelteosw/eggy/internal/adapters/sessions/jsonfile"
	skillsadapter "github.com/nigelteosw/eggy/internal/adapters/skills"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	mcpadapter "github.com/nigelteosw/eggy/internal/adapters/tools/mcp"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

type AppOptions struct {
	HTTPClient       *http.Client
	TelegramBaseURL  string
	ProviderBaseURLs map[string]string
	GitHubAPIBase    string
	GoogleAuthURL    string
	GoogleTokenURL   string
	GoogleAPIBase    string
	ConfigPath       string
	Now              func() time.Time
	Logger           *slog.Logger
	FakeAdapters     bool
	RequestRestart   func()
}

type App struct {
	config              Config
	store               ports.StateStore
	context             ports.ContextStore
	channel             ports.Channel
	dispatcher          *services.Dispatcher
	httpHandler         http.Handler
	loop                *agent.Loop
	implementationLoop  *agent.Loop
	agentRuntime        *services.AgentRuntime
	manifest            agent.CapabilityManifest
	commands            *CommandService
	scheduler           *schedulerlocal.Scheduler
	heartbeat           *services.HeartbeatPolicy
	approvals           *services.ApprovalService
	approvalExecutors   map[approvals.Action]ApprovalExecutor
	coding              *services.CodingService
	shipping            *services.ShippingService
	calendar            *services.CalendarService
	mcp                 *mcpadapter.Manager
	repositoriesService *services.RepositoriesService
	skillsService       *services.SkillsService
	conversation        *services.ConversationService
	now                 func() time.Time
	eventQueue          chan events.Event
	workers             sync.WaitGroup
	readyLog            sync.Once
	logger              *slog.Logger
	timezone            string
	location            *time.Location
}

type ApprovalExecutor interface {
	ExecuteApproved(context.Context, approvals.Approval) (any, error)
}

func NewApp(config Config, secrets Secrets, options AppOptions) (*App, error) {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.TelegramBaseURL == "" {
		options.TelegramBaseURL = "https://api.telegram.org"
	}
	if options.GitHubAPIBase == "" {
		options.GitHubAPIBase = "https://api.github.com"
	}
	if options.GoogleAuthURL == "" {
		options.GoogleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if options.GoogleTokenURL == "" {
		options.GoogleTokenURL = "https://oauth2.googleapis.com/token"
	}
	if options.GoogleAPIBase == "" {
		options.GoogleAPIBase = "https://www.googleapis.com/calendar/v3"
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	timezone := strings.TrimSpace(config.Calendar.Timezone)
	if timezone == "" {
		timezone = config.Scheduler.QuietHours.Timezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load owner timezone: %w", err)
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return nil, err
	}
	statePath := filepath.Join(config.DataDir, "state.json")
	_, statErr := os.Stat(statePath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat state: %w", statErr)
	}
	sessionStore := sessionjson.Open(filepath.Join(config.DataDir, "sessions"))
	if _, err := importLegacyCodingRuns(context.Background(), statePath, sessionStore, options.Now); err != nil {
		return nil, fmt.Errorf("import legacy coding runs: %w", err)
	}
	stateStore := jsonfile.Open(statePath)
	contextStore := contextmarkdown.Open(config.DataDir, 64<<10)
	app := &App{config: config, store: stateStore, context: contextStore, scheduler: schedulerlocal.New(stateStore), now: options.Now, eventQueue: make(chan events.Event, 64), logger: options.Logger, timezone: timezone, location: location}
	if errors.Is(statErr, os.ErrNotExist) && len(config.Repositories) > 0 {
		seeded := map[string]ports.Repository{}
		for _, configured := range config.Repositories {
			seeded[configured.Name] = ports.Repository{Name: configured.Name, CloneURL: configured.CloneURL, BaseBranch: configured.BaseBranch, ProtectedBranches: configured.ProtectedBranches}
		}
		initial, err := stateStore.Load(context.Background())
		if err != nil {
			return nil, err
		}
		if _, err := stateStore.Update(context.Background(), initial.Version, func(state *ports.State) error {
			state.Repositories = seeded
			return nil
		}); err != nil {
			return nil, fmt.Errorf("seed first-boot repositories: %w", err)
		}
	}
	var telegramClient *telegram.Client
	if options.FakeAdapters {
		app.channel = noopChannel{}
	} else {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
		app.channel = telegramClient
	}
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute)
	allowedEnvironment := append([]string(nil), config.Runner.AllowedEnv...)
	allowedEnvironment = append(allowedEnvironment, "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT")
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	repositoryCapabilities := repositoryAdapter.RepositoryCapabilities()
	activeSecrets := []string{secrets.TelegramBotToken, secrets.TelegramWebhookSecret, secrets.GitHubToken, secrets.GoogleClientID, secrets.GoogleClientSecret, secrets.EncryptionKey}
	for _, secret := range secrets.ProviderAPIKeys {
		activeSecrets = append(activeSecrets, secret)
	}
	for _, secret := range secrets.MCPBearerTokens {
		activeSecrets = append(activeSecrets, secret)
	}
	sessions := services.NewImplementationSessions(sessionStore, services.SessionPolicy{
		ContextBudgetChars: config.ImplementationSessions.ContextBudgetChars,
		RecentMessages:     config.ImplementationSessions.RecentMessages,
		OutputExcerptChars: config.ImplementationSessions.OutputExcerptChars,
	}, options.Now, activeSecrets...)
	app.shipping = services.NewShippingService(stateStore, sessions, app.approvals, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryCapabilities)
	app.shipping.SetApprovalRequester(app.approvals)
	app.shipping.SetApprovalDecider(app.approvals)
	app.repositoriesService = services.NewRepositoriesService(stateStore, runner, repositoryAdapter, app.approvals, app.approvals, repositoryCapabilities, newRunID, sessions)
	skillsStore := skillsadapter.Open(filepath.Join(config.DataDir, "skills"), 32<<10)
	app.skillsService = services.NewSkillsService(skillsStore, stateStore, app.approvals, app.approvals, services.NewSecretGuard(activeSecrets))
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{
		approvals.Commit:        app.shipping,
		approvals.Push:          app.shipping,
		approvals.CreatePR:      app.shipping,
		approvals.AddRepository: app.repositoriesService,
		approvals.SkillWrite:    app.skillsService,
		approvals.SkillDelete:   app.skillsService,
	}
	app.conversation = services.NewConversationService(stateStore, 20)

	aliases := make([]string, 0, len(config.ModelAliases))
	targets := make(map[string]agent.ModelTarget, len(config.ModelAliases))
	providerModels := make(map[string]ports.Model, len(config.Providers))
	for name, provider := range config.Providers {
		if options.FakeAdapters {
			providerModels[name] = staticModel{}
			continue
		}
		baseURL := provider.BaseURL
		if override := options.ProviderBaseURLs[name]; override != "" {
			baseURL = override
		}
		switch provider.Adapter {
		case "openai_compatible":
			providerModels[name] = openaicompat.New(baseURL, secrets.ProviderAPIKeys[name], options.HTTPClient)
		default:
			return nil, fmt.Errorf("provider %q has unsupported adapter %q", name, provider.Adapter)
		}
	}
	efforts := make(map[string][]string, len(config.ModelAliases))
	for alias, configured := range config.ModelAliases {
		model := providerModels[configured.Provider]
		if model == nil {
			return nil, fmt.Errorf("model alias %q provider %q is unavailable", alias, configured.Provider)
		}
		aliases = append(aliases, alias)
		targets[alias] = agent.ModelTarget{Model: model, ModelID: configured.Model}
		if len(configured.ReasoningEfforts) > 0 {
			efforts[alias] = configured.ReasoningEfforts
		}
	}
	sort.Strings(aliases)
	app.agentRuntime = services.NewAgentRuntime(stateStore, config.Agent.DefaultModel, aliases, efforts)
	app.implementationLoop = agent.NewSelectedLoop(targets, services.NewImplementationTools(runner, repositoryAdapter), 48)
	implementer := services.NewNativeImplementer(app.implementationLoop, func(ctx context.Context) (string, string, error) {
		alias, err := app.agentRuntime.SelectedModel(ctx)
		if err != nil {
			return "", "", err
		}
		effort, err := app.agentRuntime.ReasoningEffort(ctx)
		return alias, effort, err
	})
	registry := services.NewToolRegistry()
	app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, implementer, options.Now, sessions, app.approvals)
	owner := strconv.FormatInt(config.Telegram.OwnerID, 10)
	baseTools := []ports.Tool{services.NewStatusTool(stateStore, sessions), currentTimeTool(options.Now, location, timezone)}
	baseTools = append(baseTools, services.NewContextTools(contextStore, services.NewSecretGuard(activeSecrets))...)
	baseTools = append(baseTools, services.NewSkillTools(app.skillsService)...)
	baseTools = append(baseTools, skillProposeTool(app.skillsService, app.channel, owner))
	for _, tool := range baseTools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	progress := telegram.NewProgressTracker(app.channel, owner)
	for _, tool := range services.NewRepositoryTools(stateStore, app.coding, app.shipping, newRunID, progress.Deliver) {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	for _, tool := range services.NewRepositoryReadTools(stateStore, runner, repositoryAdapter, repositoryAdapter, newRunID) {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	app.mcp, err = newMCPManager(context.Background(), config, secrets, options)
	if err != nil {
		return nil, err
	}
	keepMCP := false
	if app.mcp != nil {
		defer func() {
			if !keepMCP {
				_ = app.mcp.Close()
			}
		}()
		for _, tool := range app.mcp.Tools() {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}

	var googleStart, googleCallback http.Handler
	if config.Calendar.Enabled {
		cipher, err := google.NewTokenCipher(secrets.EncryptionKey)
		if err != nil {
			return nil, err
		}
		googleAdapter := google.NewAdapter(google.AdapterConfig{ClientID: secrets.GoogleClientID, ClientSecret: secrets.GoogleClientSecret, RedirectURL: config.Server.PublicBaseURL + "/auth/google/callback", AuthURL: options.GoogleAuthURL, TokenURL: options.GoogleTokenURL, APIBase: options.GoogleAPIBase, Cipher: cipher, Store: stateStore, HTTPClient: options.HTTPClient})
		app.calendar = services.NewCalendarService(googleAdapter, app.approvals, app.approvals)
		app.approvalExecutors[approvals.CalendarCreate] = app.calendar
		app.approvalExecutors[approvals.CalendarUpdate] = app.calendar
		app.approvalExecutors[approvals.CalendarDelete] = app.calendar
		key, err := base64.StdEncoding.DecodeString(secrets.EncryptionKey)
		if err != nil {
			return nil, err
		}
		googleStart, googleCallback = google.NewOAuthHandlers(googleAdapter, stateStore, key, options.Now)
		for _, tool := range calendarTools(app.calendar, app.channel, strconv.FormatInt(config.Telegram.OwnerID, 10), config.Calendar.DefaultCalendar, options.Now, location, timezone) {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
	}
	for _, tool := range scheduleTools(app.scheduler, options.Now) {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	registeredTools := registry.Tools()
	app.loop = agent.NewSelectedLoop(targets, registeredTools, 40)
	toolNames := make([]string, 0, len(registeredTools))
	for _, tool := range registeredTools {
		toolNames = append(toolNames, tool.Definition().Name)
	}
	app.manifest = agent.CapabilityManifest{
		Tools: toolNames, CalendarEnabled: config.Calendar.Enabled,
		RepositoryCommitReady: repositoryCapabilities.Commit,
		RepositoryPushReady:   repositoryCapabilities.Push,
		PullRequestReady:      repositoryCapabilities.PullRequest,
	}
	schedulerLocation, err := time.LoadLocation(config.Scheduler.QuietHours.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load scheduler timezone: %w", err)
	}
	app.heartbeat, err = services.NewHeartbeatPolicy(config.Scheduler.QuietHours.Start, config.Scheduler.QuietHours.End, schedulerLocation, config.Scheduler.MinimumProactiveInterval.Value(), config.Scheduler.WeeklyProactiveLimit)
	if err != nil {
		return nil, err
	}
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, shipping: app.shipping, repositories: app.repositoriesService, skills: app.skillsService, agentRuntime: app.agentRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, configPath: options.ConfigPath, modelAliases: aliases, timezone: timezone, now: options.Now, restart: options.RequestRestart}
	if app.mcp != nil {
		app.commands.mcp = app.mcp
	}
	app.dispatcher = services.NewDispatcher(owner, stateStore, map[events.Type]services.EventHandler{
		events.TypeMessage: app.processEvent, events.TypeApproval: app.processEvent, events.TypeSchedule: app.processEvent,
		events.TypeScheduledMessage: app.processEvent, events.TypeHeartbeat: app.processEvent,
	})
	webhook := telegram.NewWebhookHandler(config.Telegram.OwnerID, secrets.TelegramWebhookSecret, app.Enqueue)
	webHandler := NewWebHandler(options.ConfigPath, WebUIConfig{
		UserEmail: secrets.UIUserEmail, Password: secrets.UIPassword,
		SigningKey: []byte(secrets.EncryptionKey), Now: options.Now,
	})
	app.httpHandler = NewHTTPHandlerAt(config.Server.TelegramWebhookPath, app.Ready, webhook, googleStart, googleCallback, webHandler, mcpCallbackHandler(app.mcp, options.RequestRestart))
	if telegramClient != nil {
		autocomplete := TelegramAutocomplete()
		commands := make([]telegram.BotCommand, 0, len(autocomplete))
		for _, command := range autocomplete {
			commands = append(commands, telegram.BotCommand{Name: command.Name, Description: command.Description})
		}
		if err := telegramClient.SetCommands(context.Background(), commands); err != nil {
			app.logger.Warn("failed to register Telegram command suggestions", "error", err)
		}
	}
	keepMCP = true
	return app, nil
}

func (a *App) Handler() http.Handler { return a.httpHandler }
func (a *App) ExecuteCommand(ctx context.Context, command string) (string, bool, error) {
	return a.commands.Execute(ctx, command)
}

// ExecuteCLI parses and dispatches conventional CLI arguments (see
// CommandService.ExecuteCLI) through this App's full runtime.
func (a *App) ExecuteCLI(ctx context.Context, args []string) (CommandResult, bool, error) {
	return a.commands.ExecuteCLI(ctx, args)
}
func (a *App) Ready() error {
	state, err := a.store.Load(context.Background())
	if err != nil {
		return err
	}
	if _, err := a.context.Load(context.Background()); err != nil {
		return err
	}
	a.readyLog.Do(func() {
		alias := a.config.Agent.DefaultModel
		provider := a.config.ModelAliases[alias].Provider
		repositories := repositoryNamesFromState(state)
		sort.Strings(repositories)
		integrations := []string{"telegram", "model_provider"}
		if len(state.Repositories) > 0 {
			integrations = append(integrations, "github")
		}
		if a.config.Calendar.Enabled {
			integrations = append(integrations, "google_calendar")
		}
		sort.Strings(integrations)
		a.logger.Info("agent runtime ready", "model_alias", alias, "provider", provider, "repositories", repositories, "integrations", integrations, "context_files", []string{"SOUL.md", "USER.md", "MEMORY.md"})
	})
	return nil
}
func (a *App) HandleEvent(ctx context.Context, event events.Event) error {
	return a.dispatcher.Handle(ctx, event)
}

func (a *App) Enqueue(ctx context.Context, event events.Event) error {
	select {
	case a.eventQueue <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("event queue is full")
	}
}

func (a *App) processEvent(ctx context.Context, event events.Event) error {
	switch event.Type {
	case events.TypeMessage:
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		return a.handleMessage(ctx, message, agent.RunOptions{}, true)
	case events.TypeSchedule:
		// A scheduled agent turn is self-contained: it starts with no ambient
		// recent-conversation history, so an owner's earlier chat cannot
		// silently steer instructions the owner never reviewed at the time
		// this schedule fires.
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		return a.handleMessage(ctx, message, readOnlyRunOptions(), false)
	case events.TypeScheduledMessage:
		// A deterministic, pre-rendered notification (a reminder or
		// watchdog-style check-in): delivered verbatim with no model call at
		// all, as distinct from TypeSchedule above.
		message, err := decodeMessage(event, a.config.Telegram.OwnerID)
		if err != nil {
			return err
		}
		return a.channel.Deliver(ctx, message.ChatID, message.Text)
	case events.TypeApproval:
		var decision events.ApprovalDecision
		if err := json.Unmarshal(event.Payload, &decision); err != nil {
			return err
		}
		return a.handleApproval(ctx, decision)
	case events.TypeHeartbeat:
		return a.handleHeartbeat(ctx)
	default:
		return errors.New("unsupported event type")
	}
}

func decodeMessage(event events.Event, ownerID int64) (events.Message, error) {
	var message events.Message
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return events.Message{}, err
	}
	if message.ChatID == "" {
		message.ChatID = strconv.FormatInt(ownerID, 10)
	}
	return message, nil
}

func readOnlyRunOptions() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true, "calendar_list": true,
		"read_file": true, "terminal": true, "repository_github": true,
		"skill_read": true,
	}}
}

// heartbeatRunOptions extends readOnlyRunOptions with the narrow memory-
// curation tools so a heartbeat turn can proactively write stable facts to
// USER.md/MEMORY.md, mirroring Hermes's periodic-nudge curation without
// adding a separate subsystem: it is the same explicit, guarded tool call a
// direct conversation turn can already make.
func heartbeatRunOptions() agent.RunOptions {
	options := readOnlyRunOptions()
	for _, tool := range []string{
		"user_append", "user_replace_section", "user_remove_section", "user_read",
		"memory_append", "memory_replace_section", "memory_remove_section", "memory_read",
		"skill_disable", "skill_enable",
	} {
		options.AllowedTools[tool] = true
	}
	return options
}

func (a *App) handleMessage(ctx context.Context, message events.Message, options agent.RunOptions, includeRecentHistory bool) error {
	if output, handled, err := a.commands.Execute(ctx, message.Text); handled {
		if err != nil {
			return err
		}
		return a.channel.Deliver(ctx, message.ChatID, output)
	}
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		return err
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := a.agentRuntime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := a.agentRuntime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := a.skillsService.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := a.capabilityManifest(state, alias, enabledSkills)
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	if includeRecentHistory {
		history = append(history, state.RecentMessages...)
	}
	stopTyping := telegram.StartTyping(ctx, a.channel, message.ChatID, 4*time.Second)
	result, runErr := a.loop.RunSelected(ctx, alias, effort, message.Text, history, options)
	stopTyping()
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
	if errors.Is(runErr, agent.ErrToolStepLimit) {
		if usageErr != nil {
			return usageErr
		}
		return a.channel.Deliver(ctx, message.ChatID, "I ran out of tool-call steps working on that before I could finish. Try a narrower request, or ask me to continue.")
	}
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if err := a.conversation.Record(ctx, ports.Message{Role: ports.RoleUser, Content: message.Text}); err != nil {
		return err
	}
	if err := a.conversation.Record(ctx, result.Message); err != nil {
		return err
	}
	if strings.TrimSpace(result.ReasoningContent) != "" {
		showThinking, err := a.agentRuntime.ShowThinking(ctx)
		if err != nil {
			return err
		}
		if showThinking {
			if err := a.channel.Deliver(ctx, message.ChatID, "Thinking:\n"+result.ReasoningContent); err != nil {
				return err
			}
		}
	}
	return a.channel.Deliver(ctx, message.ChatID, result.Message.Content)
}

func (a *App) handleApproval(ctx context.Context, decision events.ApprovalDecision) error {
	chatID := strconv.FormatInt(a.config.Telegram.OwnerID, 10)
	if decision.CallbackQueryID != "" {
		_ = a.channel.AnswerCallback(ctx, decision.CallbackQueryID)
	}
	if err := a.approvals.Decide(ctx, decision.ApprovalID, decision.Approved); err != nil {
		return err
	}
	if !decision.Approved {
		state, _ := a.store.Load(ctx)
		approval := state.Approvals[decision.ApprovalID]
		if approval.Action == approvals.Commit || approval.Action == approvals.Push || approval.Action == approvals.CreatePR {
			var payload struct{ RunID string }
			_ = json.Unmarshal(approval.Payload, &payload)
			if payload.RunID != "" {
				_ = a.coding.Cleanup(ctx, payload.RunID)
			}
		}
		return telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, "Action rejected.")
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	approval := state.Approvals[decision.ApprovalID]
	executor, ok := a.approvalExecutors[approval.Action]
	if !ok {
		return errors.New("unknown approval action")
	}
	result, err := executor.ExecuteApproved(ctx, approval)
	if err != nil {
		return err
	}
	// Commit, push, and pull-request creation no longer pause for individual
	// owner taps: ShippingService.Ship decides and executes that whole chain
	// itself (see repository_tools.go and the /continue command). These
	// branches only remain reachable for a pending approval left over from
	// before that change.
	if approval.Action == approvals.CreatePR {
		var payload struct{ RunID string }
		_ = json.Unmarshal(approval.Payload, &payload)
		if payload.RunID != "" {
			_ = a.coding.Cleanup(ctx, payload.RunID)
		}
	}
	return telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, fmt.Sprintf("Approved action completed: %v", result))
}

func (a *App) Run(ctx context.Context) error {
	defer a.workers.Wait()
	if a.mcp != nil {
		defer a.mcp.Close()
	}
	if _, err := a.coding.RecoverInterrupted(ctx); err != nil {
		return err
	}
	if err := a.scheduler.Recover(ctx); err != nil {
		return err
	}
	scheduleTicker := time.NewTicker(time.Minute)
	defer scheduleTicker.Stop()
	heartbeatCadence := a.config.Scheduler.HeartbeatCadence.Value()
	if heartbeatCadence <= 0 {
		heartbeatCadence = 30 * time.Minute
	}
	heartbeatTicker := time.NewTicker(heartbeatCadence)
	defer heartbeatTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-a.eventQueue:
			a.workers.Add(1)
			go func() {
				defer a.workers.Done()
				if err := a.HandleEvent(ctx, event); err != nil {
					slog.Error("event failed", "event_id", event.ID, "correlation_id", event.CorrelationID, "error", err)
				}
			}()
		case now := <-scheduleTicker.C:
			if err := a.coding.CleanupExpired(ctx, now.Add(-a.config.Runner.Retention.Value())); err != nil {
				return err
			}
			due, err := a.scheduler.Due(ctx, now)
			if err != nil {
				return err
			}
			for _, schedule := range due {
				schedule := schedule
				// A ScheduleExecutionMessage schedule is a deterministic,
				// pre-rendered notification (reminder or watchdog): it is
				// delivered verbatim on TypeScheduledMessage with no model
				// call. Everything else starts a self-contained,
				// no-ambient-history agent turn on TypeSchedule.
				eventType := events.TypeSchedule
				if schedule.Execution == ports.ScheduleExecutionMessage {
					eventType = events.TypeScheduledMessage
				}
				payload, _ := json.Marshal(events.Message{ChatID: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Text: schedule.Instruction})
				event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: eventType, Owner: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Timestamp: now, Payload: payload}
				a.workers.Add(1)
				go func() {
					defer a.workers.Done()
					if err := a.HandleEvent(ctx, event); err != nil {
						if failErr := a.scheduler.Fail(ctx, schedule.ID, schedule.PendingRun); failErr != nil {
							slog.Error("schedule failure acknowledgement failed", "schedule_id", schedule.ID, "error", failErr)
						}
						slog.Error("scheduled event failed", "schedule_id", schedule.ID, "error", err)
						return
					}
					if err := a.scheduler.Complete(ctx, schedule.ID, schedule.PendingRun, a.now()); err != nil {
						slog.Error("schedule completion acknowledgement failed", "schedule_id", schedule.ID, "error", err)
					}
				}()
			}
		case now := <-heartbeatTicker.C:
			_ = a.HandleEvent(ctx, events.Event{ID: "heartbeat:" + now.Format(time.RFC3339Nano), Type: events.TypeHeartbeat, Owner: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Timestamp: now, Payload: json.RawMessage(`{}`)})
		}
	}
}

// hasActiveProtectedWork reports whether an implementation run is currently
// executing. A heartbeat tick is skipped entirely while one is active rather
// than interleaving a curation/check-in turn with it.
func (a *App) hasActiveProtectedWork(ctx context.Context) (bool, error) {
	sessions, err := a.coding.List(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Phase == ports.PhaseRunning {
			return true, nil
		}
	}
	return false, nil
}

// handleHeartbeat runs a small, self-contained heartbeat turn: no ambient
// recent-conversation history, so instructions from an old chat cannot be
// silently revived. Its context is the durable docs (SOUL/USER/MEMORY), the
// owner-editable HEARTBEAT.md checklist, and the capability manifest — never
// state.RecentMessages.
//
// Silent context curation (USER.md/MEMORY.md) is never gated by quiet hours
// or the weekly proactive-message limit; only the owner-facing Telegram
// check-in is. HeartbeatPolicy.CanSend governs sending the check-in and
// recording it against the weekly limit, not whether the turn runs at all.
func (a *App) handleHeartbeat(ctx context.Context) error {
	if active, err := a.hasActiveProtectedWork(ctx); err != nil {
		return err
	} else if active {
		return nil
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	sendAllowed := a.heartbeat.CanSend(state, a.now())
	agentContext, err := a.context.Load(ctx)
	if err != nil {
		return err
	}
	alias, err := a.agentRuntime.SelectedModel(ctx)
	if err != nil {
		return err
	}
	effort, err := a.agentRuntime.ReasoningEffort(ctx)
	if err != nil {
		return err
	}
	enabledSkills, err := a.skillsService.Enabled(ctx)
	if err != nil {
		return err
	}
	manifest := a.capabilityManifest(state, alias, enabledSkills)
	options := heartbeatRunOptions()
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	history = append(history, agent.HeartbeatChecklistMessage(agentContext.Heartbeat))
	history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Heartbeat context only: an isolated turn with no recent-conversation history. Protected writes are forbidden."})
	instruction := "Separately, review durable context for any stable fact, preference, or decision worth curating into USER.md or MEMORY.md: use the read tool to see the current document first, append or replace a section for new or changed facts, and remove a section outright once it is stale, superseded, or duplicated. Curation does not require sending a check-in."
	if sendAllowed {
		instruction = "Evaluate whether one concise proactive check-in is useful now, using the HEARTBEAT.md checklist as a starting point. " + instruction + fmt.Sprintf(" Reply with exactly %q and nothing else when no check-in is useful.", services.HeartbeatNoReportSentinel)
	} else {
		instruction = "A proactive check-in cannot be sent right now (quiet hours or the proactive-message limit). Do not attempt one. " + instruction + fmt.Sprintf(" Reply with exactly %q.", services.HeartbeatNoReportSentinel)
	}
	result, runErr := a.loop.RunSelected(ctx, alias, effort, instruction, history, options)
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if !sendAllowed || services.HeartbeatHasNothingToReport(result.Message.Content) {
		return nil
	}
	if err := a.heartbeat.Record(ctx, a.store, a.now()); err != nil {
		return err
	}
	ownerChatID := strconv.FormatInt(a.config.Telegram.OwnerID, 10)
	return a.channel.Deliver(ctx, ownerChatID, result.Message.Content)
}

func newRunID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

func repositoryNamesFromState(state ports.State) []string {
	names := make([]string, 0, len(state.Repositories))
	for name := range state.Repositories {
		names = append(names, name)
	}
	return names
}

func (a *App) capabilityManifest(state ports.State, activeModel string, skills []ports.SkillSummary) agent.CapabilityManifest {
	manifest := a.manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = repositoryNamesFromState(state)
	configured := len(manifest.Repositories) > 0
	manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
	manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
	manifest.PullRequestReady = configured && manifest.PullRequestReady
	manifest.Skills = make([]agent.SkillDescriptor, 0, len(skills))
	for _, skill := range skills {
		manifest.Skills = append(manifest.Skills, agent.SkillDescriptor{Name: skill.Name, Description: skill.Description})
	}
	return manifest
}

type staticModel struct{}

func (staticModel) Generate(context.Context, ports.ModelRequest) (ports.ModelResponse, error) {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "Eggy fake adapter ready."}}, nil
}

type noopChannel struct{}

func (noopChannel) Deliver(context.Context, string, string) error                     { return nil }
func (noopChannel) DeliverApproval(context.Context, string, approvals.Approval) error { return nil }
func (noopChannel) DeliverTrackable(context.Context, string, string) (string, error)  { return "", nil }
func (noopChannel) EditText(context.Context, string, string, string) error            { return nil }
func (noopChannel) AnswerCallback(context.Context, string) error                      { return nil }
func (noopChannel) SendTyping(context.Context, string) error                          { return nil }
