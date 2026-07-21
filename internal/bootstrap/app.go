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
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
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
	repositoriesService *services.RepositoriesService
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
	app.shipping = services.NewShippingService(stateStore, app.approvals, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryCapabilities)
	app.shipping.SetApprovalRequester(app.approvals)
	app.shipping.SetApprovalDecider(app.approvals)
	app.repositoriesService = services.NewRepositoriesService(stateStore, runner, repositoryAdapter, app.approvals, app.approvals, repositoryCapabilities, newRunID)
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{
		approvals.Commit:        app.shipping,
		approvals.Push:          app.shipping,
		approvals.CreatePR:      app.shipping,
		approvals.AddRepository: app.repositoriesService,
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
	activeSecrets := []string{secrets.TelegramBotToken, secrets.TelegramWebhookSecret, secrets.GitHubToken, secrets.GoogleClientID, secrets.GoogleClientSecret, secrets.EncryptionKey}
	for _, secret := range secrets.ProviderAPIKeys {
		activeSecrets = append(activeSecrets, secret)
	}
	sessions := services.NewImplementationSessions(sessionjson.Open(filepath.Join(config.DataDir, "sessions")), services.SessionPolicy{
		ContextBudgetChars: config.ImplementationSessions.ContextBudgetChars,
		RecentMessages:     config.ImplementationSessions.RecentMessages,
		OutputExcerptChars: config.ImplementationSessions.OutputExcerptChars,
	}, options.Now, activeSecrets...)
	app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, implementer, options.Now, sessions, app.approvals)
	app.shipping.SetSessionLifecycle(sessions)
	baseTools := []ports.Tool{services.NewStatusTool(stateStore), currentTimeTool(options.Now, location, timezone)}
	baseTools = append(baseTools, services.NewContextTools(contextStore, services.NewSecretGuard(activeSecrets))...)
	for _, tool := range baseTools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	owner := strconv.FormatInt(config.Telegram.OwnerID, 10)
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
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, shipping: app.shipping, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, configPath: options.ConfigPath, modelAliases: aliases, now: options.Now, restart: options.RequestRestart}
	app.dispatcher = services.NewDispatcher(owner, stateStore, map[events.Type]services.EventHandler{
		events.TypeMessage: app.processEvent, events.TypeApproval: app.processEvent, events.TypeSchedule: app.processEvent, events.TypeHeartbeat: app.processEvent,
	})
	webhook := telegram.NewWebhookHandler(config.Telegram.OwnerID, secrets.TelegramWebhookSecret, app.Enqueue)
	app.httpHandler = NewHTTPHandlerAt(config.Server.TelegramWebhookPath, app.Ready, webhook, googleStart, googleCallback)
	if telegramClient != nil {
		if err := telegramClient.SetCommands(context.Background(), telegram.Commands()); err != nil {
			app.logger.Warn("failed to register Telegram command suggestions", "error", err)
		}
	}
	return app, nil
}

func (a *App) Handler() http.Handler { return a.httpHandler }
func (a *App) ExecuteCommand(ctx context.Context, command string) (string, bool, error) {
	return a.commands.Execute(ctx, command)
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
	case events.TypeMessage, events.TypeSchedule:
		var message events.Message
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			return err
		}
		if message.ChatID == "" {
			message.ChatID = strconv.FormatInt(a.config.Telegram.OwnerID, 10)
		}
		options := agent.RunOptions{}
		if event.Type == events.TypeSchedule {
			options = readOnlyRunOptions()
		}
		return a.handleMessage(ctx, message, options)
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

func readOnlyRunOptions() agent.RunOptions {
	return agent.RunOptions{AllowedTools: map[string]bool{
		"status": true, "repository_list": true, "calendar_list": true,
		"read_file": true, "terminal": true, "repository_github": true,
	}}
}

// heartbeatRunOptions extends readOnlyRunOptions with the narrow memory-
// curation tools so a heartbeat turn can proactively write stable facts to
// USER.md/MEMORY.md, mirroring Hermes's periodic-nudge curation without
// adding a separate subsystem: it is the same explicit, guarded tool call a
// direct conversation turn can already make.
func heartbeatRunOptions() agent.RunOptions {
	options := readOnlyRunOptions()
	for _, tool := range []string{"user_append", "user_replace_section", "memory_append", "memory_replace_section"} {
		options.AllowedTools[tool] = true
	}
	return options
}

func (a *App) handleMessage(ctx context.Context, message events.Message, options agent.RunOptions) error {
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
	manifest := a.capabilityManifest(state, alias)
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	if state.ConversationSummary != "" {
		history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Conversation summary:\n" + state.ConversationSummary})
	}
	history = append(history, state.RecentMessages...)
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
		if err := a.channel.Deliver(ctx, message.ChatID, "Thinking:\n"+result.ReasoningContent); err != nil {
			return err
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
	if _, err := services.NewTaskService(a.store, a.now).RecoverInterrupted(ctx); err != nil {
		return err
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
				payload, _ := json.Marshal(events.Message{ChatID: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Text: schedule.Instruction})
				event := events.Event{ID: "schedule:" + schedule.ID + ":" + schedule.PendingRun.Format(time.RFC3339Nano), Type: events.TypeSchedule, Owner: strconv.FormatInt(a.config.Telegram.OwnerID, 10), Timestamp: now, Payload: payload}
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

func (a *App) handleHeartbeat(ctx context.Context) error {
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	if !a.heartbeat.CanSend(state, a.now()) {
		return nil
	}
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
	manifest := a.capabilityManifest(state, alias)
	options := heartbeatRunOptions()
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Heartbeat context only. Protected writes are forbidden."})
	if state.ConversationSummary != "" {
		history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Conversation summary:\n" + state.ConversationSummary})
	}
	history = append(history, state.RecentMessages...)
	instruction := "Evaluate whether one concise proactive check-in is useful now. Separately, review recent conversation for any stable fact, preference, or decision worth curating into USER.md or MEMORY.md, and curate it now via the memory tools; curation does not require sending a check-in. Return an empty response when no check-in is useful."
	result, runErr := a.loop.RunSelected(ctx, alias, effort, instruction, history, options)
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
	if runErr != nil {
		return runErr
	}
	if usageErr != nil {
		return usageErr
	}
	if strings.TrimSpace(result.Message.Content) == "" {
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

func (a *App) capabilityManifest(state ports.State, activeModel string) agent.CapabilityManifest {
	manifest := a.manifest
	manifest.ActiveModel = activeModel
	manifest.Repositories = repositoryNamesFromState(state)
	configured := len(manifest.Repositories) > 0
	manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
	manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
	manifest.PullRequestReady = configured && manifest.PullRequestReady
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
