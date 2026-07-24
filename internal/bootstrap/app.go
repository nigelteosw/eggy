package bootstrap

import (
	"context"
	"crypto/sha256"
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
	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	memorysqlite "github.com/nigelteosw/eggy/internal/adapters/memory/sqlite"
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

// This file is the composition root: AppOptions/App's shape and NewApp's
// wiring of every adapter into it, plus the handful of App methods thin
// enough to be pure delegation. App's actual runtime behavior once
// constructed -- the event loop, conversation turns, heartbeat, and
// approvals -- lives in app_events.go.

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
	config                  Config
	store                   ports.StateStore
	context                 ports.ContextStore
	channel                 ports.Channel
	chatHub                 *webchat.Hub
	dispatcher              *services.Dispatcher
	httpHandler             http.Handler
	loop                    *agent.Loop
	implementationLoop      *agent.Loop
	agentRuntime            *services.AgentRuntime
	manifest                agent.CapabilityManifest
	commands                *CommandService
	scheduler               *schedulerlocal.Scheduler
	heartbeat               *services.HeartbeatPolicy
	approvals               *services.ApprovalService
	approvalExecutors       map[approvals.Action]ApprovalExecutor
	coding                  *services.CodingService
	shipping                *services.ShippingService
	calendar                *services.CalendarService
	mcp                     *mcpadapter.Manager
	repositoriesService     *services.RepositoriesService
	skillsService           *services.SkillsService
	conversation            *services.ConversationService
	memory                  *memorysqlite.Store
	embedder                ports.Embedder
	memoryWorker            *services.MemoryEmbeddingWorker
	memoryEmbeddingInterval time.Duration
	now                     func() time.Time
	eventQueue              chan events.Event
	workers                 sync.WaitGroup
	readyLog                sync.Once
	logger                  *slog.Logger
	timezone                string
	location                *time.Location
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
	memoryStore, err := memorysqlite.OpenWithProfile(
		filepath.Join(config.DataDir, "eggy.db"),
		config.Embeddings.CandidateLimit,
		embeddingProfile(config, options),
	)
	if err != nil {
		return nil, fmt.Errorf("open conversation memory: %w", err)
	}
	keepMemory := false
	defer func() {
		if !keepMemory {
			_ = memoryStore.Close()
		}
	}()
	app := &App{
		config: config, store: stateStore, context: contextStore, scheduler: schedulerlocal.New(stateStore),
		memory: memoryStore, memoryEmbeddingInterval: time.Minute,
		now: options.Now, eventQueue: make(chan events.Event, 64), logger: options.Logger, timezone: timezone, location: location,
	}
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
	app.chatHub = webchat.NewHub()
	webChannel := webchat.New(app.chatHub)
	var telegramClient *telegram.Client
	// telegramChannel starts as a true nil ports.Channel (the zero value of
	// an interface, never assigned) when FakeAdapters is set, not a nil
	// *telegram.Client boxed into a non-nil interface -- assigning
	// telegramClient directly here even when it's nil would produce exactly
	// that trap (an interface value that compares != nil despite wrapping a
	// nil pointer), which is what newRoutedChannel's own nil checks rely on
	// NOT happening. See internal/bootstrap/mcp.go's ExecuteMCPCLI for the
	// same bug, found and fixed earlier in this project's history.
	var telegramChannel ports.Channel
	if !options.FakeAdapters {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
		telegramChannel = telegramClient
	}
	app.channel = newRoutedChannel(telegramChannel, webChannel, strconv.FormatInt(config.Telegram.OwnerID, 10))
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute)
	allowedEnvironment := append([]string(nil), config.Runner.AllowedEnv...)
	allowedEnvironment = append(allowedEnvironment, "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT")
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	repositoryCapabilities := repositoryAdapter.RepositoryCapabilities()
	activeSecrets := []string{secrets.TelegramBotToken, secrets.TelegramWebhookSecret, secrets.GitHubToken, secrets.GoogleClientID, secrets.GoogleClientSecret, secrets.EncryptionKey, secrets.UIPassword}
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
	app.repositoriesService = services.NewRepositoriesService(stateStore, runner, repositoryAdapter, app.approvals, app.approvals, repositoryCapabilities, newRunID, sessions)
	skillsStore := skillsadapter.Open(filepath.Join(config.DataDir, "skills"), 32<<10)
	app.skillsService = services.NewSkillsService(skillsStore, stateStore, app.approvals, app.approvals, services.NewSecretGuard(activeSecrets))
	// Commit, push, and pull-request creation are no longer decided by an
	// owner Telegram tap: ShippingService.Ship issues, decides, and
	// authorizes that whole chain itself (see shipping.go). Registration and
	// Calendar mutations still go through this human-tap callback path.
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{
		approvals.AddRepository: app.repositoriesService,
		approvals.SkillWrite:    app.skillsService,
		approvals.SkillDelete:   app.skillsService,
	}
	app.conversation = services.NewConversationService(memoryStore, 20, options.Now, options.Logger)

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
	if config.Embeddings.Provider != "" {
		if options.FakeAdapters {
			app.embedder = deterministicEmbedder{dimensions: config.Embeddings.Dimensions}
		} else {
			provider := config.Providers[config.Embeddings.Provider]
			baseURL := provider.BaseURL
			if override := options.ProviderBaseURLs[config.Embeddings.Provider]; override != "" {
				baseURL = override
			}
			app.embedder = openaicompat.NewEmbedder(
				baseURL,
				secrets.ProviderAPIKeys[config.Embeddings.Provider],
				config.Embeddings.Model,
				config.Embeddings.Dimensions,
				options.HTTPClient,
			)
		}
		app.memoryWorker = services.NewMemoryEmbeddingWorker(memoryStore, app.embedder, 0)
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
	baseTools := []ports.Tool{
		services.NewStatusTool(stateStore, sessions),
		currentTimeTool(options.Now, location, timezone),
		services.NewRecallConversationTool(memoryStore, app.embedder, services.NewSecretGuard(activeSecrets)),
	}
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
		ChatHub: app.chatHub, Enqueue: app.Enqueue, Memory: memoryStore, OwnerID: owner,
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
	keepMemory = true
	return app, nil
}

func embeddingProfile(config Config, options AppOptions) string {
	if config.Embeddings.Provider == "" {
		return ""
	}
	provider := config.Providers[config.Embeddings.Provider]
	baseURL := provider.BaseURL
	if override := options.ProviderBaseURLs[config.Embeddings.Provider]; override != "" {
		baseURL = override
	}
	encoded, _ := json.Marshal(struct {
		Provider   string `json:"provider"`
		Adapter    string `json:"adapter"`
		BaseURL    string `json:"base_url"`
		Model      string `json:"model"`
		Dimensions int    `json:"dimensions"`
	}{
		Provider:   config.Embeddings.Provider,
		Adapter:    provider.Adapter,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Model:      config.Embeddings.Model,
		Dimensions: config.Embeddings.Dimensions,
	})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
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

type staticModel struct{}

func (staticModel) Generate(context.Context, ports.ModelRequest) (ports.ModelResponse, error) {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "Eggy fake adapter ready."}}, nil
}

type deterministicEmbedder struct {
	dimensions int
}

func (e deterministicEmbedder) Embed(_ context.Context, input string) ([]float32, error) {
	embedding := make([]float32, e.dimensions)
	for index, value := range []byte(input) {
		embedding[(index+int(value))%e.dimensions]++
	}
	return embedding, nil
}

type noopChannel struct{}

func (noopChannel) Deliver(context.Context, string, string) error                     { return nil }
func (noopChannel) DeliverApproval(context.Context, string, approvals.Approval) error { return nil }
func (noopChannel) DeliverTrackable(context.Context, string, string) (string, error)  { return "", nil }
func (noopChannel) EditText(context.Context, string, string, string) error            { return nil }
func (noopChannel) AnswerCallback(context.Context, string) error                      { return nil }
func (noopChannel) SendTyping(context.Context, string) error                          { return nil }
