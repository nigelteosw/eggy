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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/calendar/google"
	"github.com/nigelteosw/eggy/internal/adapters/channels/telegram"
	"github.com/nigelteosw/eggy/internal/adapters/coding/claudecli"
	"github.com/nigelteosw/eggy/internal/adapters/coding/codexcli"
	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/models/openaicompat"
	githubadapter "github.com/nigelteosw/eggy/internal/adapters/repositories/github"
	"github.com/nigelteosw/eggy/internal/adapters/runner/localprocess"
	schedulerlocal "github.com/nigelteosw/eggy/internal/adapters/scheduler/local"
	"github.com/nigelteosw/eggy/internal/adapters/state/jsonfile"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/lane"
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
	CodexExecutable  string
	ClaudeExecutable string
	ConfigPath       string
	Now              func() time.Time
	Logger           *slog.Logger
	FakeAdapters     bool
}

type App struct {
	config              Config
	store               ports.StateStore
	context             ports.ContextStore
	channel             ports.Channel
	dispatcher          *services.Dispatcher
	httpHandler         http.Handler
	loop                *agent.Loop
	agentRuntime        *services.AgentRuntime
	manifest            agent.CapabilityManifest
	commands            *CommandService
	scheduler           *schedulerlocal.Scheduler
	heartbeat           *services.HeartbeatPolicy
	approvals           *services.ApprovalService
	approvalExecutors   map[approvals.Action]ApprovalExecutor
	coding              *services.CodingService
	codingRuntime       *services.CodingAgentRuntime
	codingAliases       []string
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
	if options.CodexExecutable == "" {
		options.CodexExecutable = "codex"
	}
	if options.ClaudeExecutable == "" {
		options.ClaudeExecutable = "claude"
	}
	if config.Coding.DefaultAgent == "" && len(config.Coding.Agents) == 0 {
		config.Coding = defaultCodingConfig()
	}
	availableCoding := make(map[string]CodingAgentConfig, len(config.Coding.Agents))
	codingExecutables := make(map[string]string, len(config.Coding.Agents))
	codingAliases := make([]string, 0, len(config.Coding.Agents))
	configuredAliases := make([]string, 0, len(config.Coding.Agents))
	for alias := range config.Coding.Agents {
		configuredAliases = append(configuredAliases, alias)
	}
	sort.Strings(configuredAliases)
	for _, alias := range configuredAliases {
		configured := config.Coding.Agents[alias]
		credential := secrets.CodingAgentCredentials[alias]
		if configured.CredentialEnv != "" && strings.TrimSpace(credential) == "" {
			if alias == config.Coding.DefaultAgent {
				return nil, fmt.Errorf("default coding agent %q is unavailable: required credential %s is missing", alias, configured.CredentialEnv)
			}
			continue
		}
		var executable string
		switch configured.Adapter {
		case "codex_cli":
			executable = options.CodexExecutable
		case "claude_cli":
			executable = options.ClaudeExecutable
		default:
			return nil, fmt.Errorf("coding agent alias %q uses unsupported adapter %q", alias, configured.Adapter)
		}
		if !options.FakeAdapters && len(config.Repositories) > 0 {
			if _, err := exec.LookPath(executable); err != nil {
				if alias == config.Coding.DefaultAgent {
					return nil, fmt.Errorf("default coding agent %q is unavailable: executable %q: %w", alias, executable, err)
				}
				continue
			}
		}
		availableCoding[alias] = configured
		codingExecutables[alias] = executable
		codingAliases = append(codingAliases, alias)
	}
	if _, ok := availableCoding[config.Coding.DefaultAgent]; !ok {
		return nil, fmt.Errorf("default coding agent %q is unavailable", config.Coding.DefaultAgent)
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
	for _, alias := range codingAliases {
		directory := "codex"
		if availableCoding[alias].Adapter == "claude_cli" {
			directory = "claude"
		}
		if err := os.MkdirAll(filepath.Join(config.DataDir, directory), 0o700); err != nil {
			return nil, err
		}
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
	for _, alias := range codingAliases {
		switch availableCoding[alias].Adapter {
		case "codex_cli":
			allowedEnvironment = append(allowedEnvironment, "CODEX_HOME")
		case "claude_cli":
			allowedEnvironment = append(allowedEnvironment, "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR")
		}
	}
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	repositoryCapabilities := repositoryAdapter.RepositoryCapabilities()
	codingAgents := make(map[string]ports.CodingAgent, len(codingAliases))
	for _, alias := range codingAliases {
		switch availableCoding[alias].Adapter {
		case "codex_cli":
			codingAgents[alias] = codexcli.New(codingExecutables[alias], runner, config.Runner.MaxOutputBytes, filepath.Join(config.DataDir, "codex"))
		case "claude_cli":
			codingAgents[alias] = claudecli.New(codingExecutables[alias], runner, config.Runner.MaxOutputBytes, secrets.CodingAgentCredentials[alias], filepath.Join(config.DataDir, "claude"))
		}
	}
	app.codingRuntime, err = services.NewCodingAgentRuntime(stateStore, config.Coding.DefaultAgent, codingAgents)
	if err != nil {
		return nil, err
	}
	app.codingAliases = app.codingRuntime.Aliases()
	app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, app.codingRuntime, options.Now)
	app.shipping = services.NewShippingService(stateStore, app.approvals, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryAdapter, repositoryCapabilities)
	app.shipping.SetApprovalRequester(app.approvals)
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
		providerModels[name] = openaicompat.New(baseURL, secrets.ProviderAPIKeys[name], options.HTTPClient)
	}
	for alias, configured := range config.ModelAliases {
		model := providerModels[configured.Provider]
		if model == nil {
			return nil, fmt.Errorf("model alias %q provider %q is unavailable", alias, configured.Provider)
		}
		aliases = append(aliases, alias)
		targets[alias] = agent.ModelTarget{Model: model, ModelID: configured.Model}
	}
	sort.Strings(aliases)
	app.agentRuntime = services.NewAgentRuntime(stateStore, config.Agent.DefaultModel, aliases)
	registry := services.NewToolRegistry()
	activeSecrets := []string{secrets.TelegramBotToken, secrets.TelegramWebhookSecret, secrets.GitHubToken, secrets.GoogleClientID, secrets.GoogleClientSecret, secrets.EncryptionKey}
	for _, secret := range secrets.ProviderAPIKeys {
		activeSecrets = append(activeSecrets, secret)
	}
	baseTools := []ports.Tool{services.NewStatusTool(stateStore), currentTimeTool(options.Now, location, timezone)}
	baseTools = append(baseTools, services.NewContextTools(contextStore, services.NewSecretGuard(activeSecrets))...)
	for _, tool := range baseTools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	owner := strconv.FormatInt(config.Telegram.OwnerID, 10)
	progress := telegram.NewProgressTracker(app.channel, owner)
	for _, tool := range services.NewRepositoryTools(stateStore, app.coding, app.shipping, newRunID,
		progress.Deliver,
		func(ctx context.Context, approval approvals.Approval) error {
			return app.channel.DeliverApproval(ctx, owner, approval)
		},
	) {
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
	app.loop = agent.NewSelectedLoop(targets, registeredTools, []string{"repository_modify"}, 8)
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
	app.commands = &CommandService{config: config, store: stateStore, context: contextStore, conversation: app.conversation, coding: app.coding, repositories: app.repositoriesService, agentRuntime: app.agentRuntime, codingRuntime: app.codingRuntime, channel: app.channel, owner: owner, defaultModel: config.Agent.DefaultModel, defaultCodingAgent: config.Coding.DefaultAgent, configPath: options.ConfigPath, modelAliases: aliases, now: options.Now}
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
			integrations = append(integrations, a.codingAliases...)
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
		return a.handleMessage(ctx, message, eventLane(event.Type, message.Text))
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

func eventLane(eventType events.Type, text string) lane.Lane {
	if eventType != events.TypeMessage {
		return lane.Assistant
	}
	return lane.Detect(text)
}

func (a *App) handleMessage(ctx context.Context, message events.Message, turnLane lane.Lane) error {
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
	manifest := a.capabilityManifest(state, alias)
	options := agent.RunOptions{Lane: turnLane}
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	if state.ConversationSummary != "" {
		history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Conversation summary:\n" + state.ConversationSummary})
	}
	history = append(history, state.RecentMessages...)
	stopTyping := telegram.StartTyping(ctx, a.channel, message.ChatID, 4*time.Second)
	result, runErr := a.loop.RunSelected(ctx, alias, message.Text, history, options)
	stopTyping()
	usageErr := a.agentRuntime.RecordUsage(ctx, alias, result.Usage)
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
	if approval.Action == approvals.Commit {
		var payload struct{ RunID string }
		_ = json.Unmarshal(approval.Payload, &payload)
		updated, _ := a.store.Load(ctx)
		run := updated.CodingRuns[payload.RunID]
		next, err := a.shipping.RequestPush(ctx, payload.RunID, run.Branch)
		if err != nil {
			if errors.Is(err, services.ErrRepositoryPushUnavailable) {
				return telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, fmt.Sprintf("Committed %v. Push is unavailable for the configured repository provider.", result))
			}
			return err
		}
		if err := telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, fmt.Sprintf("Committed %v.", result)); err != nil {
			return err
		}
		return a.channel.DeliverApproval(ctx, chatID, next)
	}
	if approval.Action == approvals.Push {
		var payload struct{ RunID, Branch string }
		_ = json.Unmarshal(approval.Payload, &payload)
		next, err := a.shipping.RequestPullRequest(ctx, payload.RunID, payload.Branch, "Eggy: "+payload.Branch, "Automated by Eggy after explicit owner approvals.")
		if err != nil {
			if errors.Is(err, services.ErrPullRequestUnavailable) {
				return telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, "Push completed. Pull-request creation is unavailable for the configured repository provider.")
			}
			return err
		}
		if err := telegram.DeliverOutcome(ctx, a.channel, chatID, decision.MessageID, "Push completed."); err != nil {
			return err
		}
		return a.channel.DeliverApproval(ctx, chatID, next)
	}
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
	manifest := a.capabilityManifest(state, alias)
	allowed := map[string]bool{
		"status": true, "repository_list": true, "calendar_list": true,
		"repository_tree": true, "repository_search": true, "repository_read": true, "repository_status": true, "repository_github": true,
	}
	options := agent.RunOptions{AllowedTools: allowed}
	manifest.Tools = a.loop.ToolNames(options)
	history := agent.BuildInstructions(agentContext, manifest, agent.TemporalContext{Now: a.now().In(a.location), Timezone: a.timezone})
	history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Heartbeat context only. Protected writes are forbidden."})
	result, runErr := a.loop.RunSelected(ctx, alias, "Evaluate whether one concise proactive check-in is useful now. Return an empty response when none is useful.", history, options)
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
	return a.channel.Deliver(ctx, strconv.FormatInt(a.config.Telegram.OwnerID, 10), result.Message.Content)
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
	manifest.ActiveCodingAgent = state.Coding.SelectedAgent
	if manifest.ActiveCodingAgent == "" {
		manifest.ActiveCodingAgent = a.config.Coding.DefaultAgent
	}
	manifest.Repositories = repositoryNamesFromState(state)
	configured := len(manifest.Repositories) > 0
	available := false
	for _, alias := range a.codingAliases {
		if alias == manifest.ActiveCodingAgent {
			available = true
			break
		}
	}
	manifest.CodingAgentReady = configured && available
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
