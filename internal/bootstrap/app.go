package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/kernel/services/repo"
	"github.com/nigelteosw/eggy/internal/kernel/turns"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/internal/web"
	"github.com/nigelteosw/eggy/plugins/channels/telegram"
	"github.com/nigelteosw/eggy/plugins/channels/webchat"
	memorysqlite "github.com/nigelteosw/eggy/plugins/memory/sqlite"
	githubadapter "github.com/nigelteosw/eggy/plugins/repositories/github"
	"github.com/nigelteosw/eggy/plugins/runner/localprocess"
	schedulerlocal "github.com/nigelteosw/eggy/plugins/scheduler/local"
	skillsadapter "github.com/nigelteosw/eggy/plugins/skills"
	mcpadapter "github.com/nigelteosw/eggy/plugins/tools/mcp"
)

// This file is the composition root: AppOptions/App's shape and NewApp's
// wiring of every adapter into it, plus the handful of App methods thin
// enough to be pure delegation. App's actual runtime behavior once
// constructed -- the event loop, conversation turns, and approvals -- lives
// in app_events.go.

type AppOptions struct {
	HTTPClient       *http.Client
	TelegramBaseURL  string
	ProviderBaseURLs map[string]string
	GitHubAPIBase    string
	ConfigPath       string
	Now              func() time.Time
	Logger           *slog.Logger
	FakeAdapters     bool
}

// maxToolStepsPerTurn is no longer a work cap. A turn that outgrows its
// context budget compacts at a checkpoint and keeps going, so this is only
// the runaway guard against a model that calls tools forever without ever
// answering.
const maxToolStepsPerTurn = 500

type App struct {
	config            config.Config
	home              home.Layout
	store             ports.StateStore
	context           ports.ContextStore
	channel           ports.Channel
	chatHub           *webchat.Hub
	dispatcher        *services.Dispatcher
	httpHandler       http.Handler
	loop              *agent.Loop
	agentRuntime      *services.AgentRuntime
	manifest          agent.CapabilityManifest
	turnService       *turns.Service
	commands          *commands.CommandService
	scheduler         *schedulerlocal.Scheduler
	approvals         *services.ApprovalService
	approvalExecutors map[approvals.Action]ApprovalExecutor
	turns             *services.ActiveTurns
	workspaces        *repo.WorkspaceSessions
	mcp               *mcpadapter.Manager
	skillsService     *services.SkillsService
	conversation      *services.ConversationService
	memory            *memorysqlite.Store
	now               func() time.Time
	eventQueue        chan events.Event
	workers           sync.WaitGroup
	readyLog          sync.Once
	logger            *slog.Logger
	timezone          string
	location          *time.Location
}

// ApprovalExecutor is an alias rather than its own interface so the executor
// map bootstrap builds is the exact type turns.Options takes.
type ApprovalExecutor = turns.ApprovalExecutor

func NewApp(config config.Config, secrets config.Secrets, options AppOptions) (*App, error) {
	options.applyDefaults()
	timezone := config.Agent.Timezone
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load owner timezone: %w", err)
	}
	opened, err := openStores(config)
	if err != nil {
		return nil, err
	}
	layout, stateStore, contextStore, memoryStore := opened.layout, opened.state, opened.context, opened.memory
	keepMemory := false
	defer func() {
		if !keepMemory {
			_ = memoryStore.Close()
		}
	}()
	app := &App{
		config: config, home: layout, store: stateStore, context: contextStore, scheduler: schedulerlocal.New(opened.cron),
		memory: memoryStore,
		now:    options.Now, eventQueue: make(chan events.Event, 64), logger: options.Logger, timezone: timezone, location: location,
	}
	configuredRepositories := map[string]ports.Repository{}
	for _, configured := range config.Repositories {
		configuredRepositories[configured.Name] = ports.Repository{Name: configured.Name, CloneURL: configured.CloneURL, BaseBranch: configured.BaseBranch, ProtectedBranches: configured.ProtectedBranches}
	}
	initial, err := stateStore.Load(context.Background())
	if err != nil {
		return nil, err
	}
	if _, err := stateStore.Update(context.Background(), initial.Version, func(state *ports.State) error {
		state.Repositories = configuredRepositories
		return nil
	}); err != nil {
		return nil, fmt.Errorf("sync configured repositories: %w", err)
	}
	app.chatHub = webchat.NewHub()
	webChannel := webchat.New(app.chatHub)
	var telegramClient *telegram.Client
	// telegramChannel starts as a true nil ports.Channel (the zero value of
	// an interface, never assigned) when FakeAdapters is set, not a nil
	// *telegram.Client boxed into a non-nil interface -- assigning
	// telegramClient directly here even when it's nil would produce an
	// interface value that compares non-nil despite wrapping a nil pointer.
	// telegramAcknowledger is kept as a separate interface variable for the
	// same reason, so NewWebhookHandler's own nil check stays meaningful.
	var telegramChannel ports.Channel
	var telegramAcknowledger telegram.CallbackAcknowledger
	var telegramSelector *telegram.Selector
	// Telegram is optional: a web-only deployment omits the config block and
	// gets no client, no channel, and no webhook route. newRoutedChannel
	// already collapses to the web channel alone when telegramChannel is a
	// true nil, and web.NewHTTPHandler already serves the webhook path as
	// unavailable when its handler is nil.
	if !options.FakeAdapters && config.Telegram.Configured() {
		telegramClient = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, strconv.FormatInt(config.Telegram.OwnerID, 10), options.HTTPClient)
		telegramChannel = telegramClient
		telegramAcknowledger = telegramClient
		telegramSelector = telegram.NewSelector(telegramClient, options.Now, 10*time.Minute)
	}
	app.channel = newRoutedChannel(telegramChannel, webChannel)
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute)
	allowedEnvironment := append([]string(nil), config.Runner.AllowedEnv...)
	allowedEnvironment = append(allowedEnvironment, "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT")
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	if !options.FakeAdapters {
		for name, repository := range configuredRepositories {
			if err := repositoryAdapter.ValidateCloneAccess(context.Background(), repository); err != nil {
				return nil, fmt.Errorf("validate repository %q: %w", name, err)
			}
		}
	}
	// The same list log redaction uses, rather than a second one assembled
	// here. When this was its own list it drifted: the Google client secret and
	// the MCP OAuth client secrets were absent, so every other credential was
	// kept out of durable context and recall while those two were not.
	activeSecrets := secrets.Values()
	skillsStore := skillsadapter.Open(layout.Skills(), 32<<10)
	app.skillsService = services.NewSkillsService(skillsStore)
	app.approvalExecutors = map[approvals.Action]ApprovalExecutor{}
	app.conversation = services.NewConversationService(memoryStore, 20, options.Now, options.Logger)

	catalog, err := buildModelCatalog(config, secrets, options)
	if err != nil {
		return nil, err
	}
	aliases, targets := catalog.aliases, catalog.targets
	app.agentRuntime = services.NewAgentRuntime(stateStore, config.Agent.DefaultModel, aliases, catalog.efforts)
	// One kernel-owned primitive set, built once and registered in the one
	// registry the one loop runs on: a primitive name resolves to exactly one
	// definition and one implementation, because there is no second loop for
	// it to mean something else in.
	app.workspaces = repo.NewWorkspaceSessions(stateStore, memoryStore, runner, repositoryAdapter, newRunID, options.Now, options.Logger)
	primitives := repo.NewPrimitiveTools(app.workspaces, repositoryAdapter)
	registry := services.NewToolRegistry()
	app.turns = services.NewActiveTurns()
	owner := config.Owner.ID
	baseTools := []ports.Tool{
		repo.NewStatusTool(stateStore, app.scheduler),
		currentTimeTool(options.Now, location, timezone),
		services.NewRecallConversationTool(memoryStore, services.NewSecretGuard(activeSecrets)),
	}
	baseTools = append(baseTools, services.NewContextTools(contextStore, services.NewSecretGuard(activeSecrets))...)
	baseTools = append(baseTools, services.NewSkillTools(app.skillsService)...)
	if err := registerAll(registry, baseTools...); err != nil {
		return nil, err
	}
	if len(config.Repositories) > 0 {
		if err := registerAll(registry, repo.NewRepositoryTools(stateStore)...); err != nil {
			return nil, err
		}
		if err := registerAll(registry, repo.NewRepositoryMetadataTools(stateStore, repositoryAdapter)...); err != nil {
			return nil, err
		}
		if err := registerAll(registry, app.workspaces.Tools()...); err != nil {
			return nil, err
		}
	}
	// Registered after every other kernel tool: the registry rejects
	// duplicates, so an adapter that tries to shadow a primitive fails
	// bootstrap rather than silently winning. MCP tools are not registered
	// here at all -- they arrive as a live provider on the same registry (see
	// AddProvider below), where the same invariant holds because a registered
	// tool always wins the name.
	if len(config.Repositories) > 0 {
		if err := registerAll(registry, primitives...); err != nil {
			return nil, err
		}
	}
	if telegramSelector != nil {
		if err := registry.Register(telegramSelector.Tool()); err != nil {
			return nil, err
		}
	}
	// One grant, several products, registered like any other kernel tool.
	// Unlike MCP these are not a live provider: the tool set is decided by
	// config.products at startup and does not change when a login completes --
	// only whether a call succeeds does.
	googleAuth, googleWorkspace, err := newGoogleWorkspace(config, secrets, options)
	if err != nil {
		return nil, err
	}
	googleAdministration := newGoogleAdmin(googleAuth)
	if err := registerAll(registry, googleTools(googleWorkspace, config.Google, options.Now)...); err != nil {
		return nil, err
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
	}

	if err := registerAll(registry, scheduleTools(app.scheduler, options.Now)...); err != nil {
		return nil, err
	}
	if app.mcp != nil {
		// The catalog is read per turn rather than copied, so a reconnected
		// server, a reloaded catalog, or a logout changes the tools the next
		// turn sees without restarting the process. It is a provider on the one
		// registry rather than a second source on the loop: MCP supplies tools,
		// it does not modify the agent.
		registry.AddProvider(app.mcp.Tools)
	}
	// One context budget for one loop: a turn compacts at a checkpoint rather than ending
	// because it did a lot of work.
	contextPolicy := agent.ContextPolicy{
		BudgetChars:        96000,
		RecentSteps:        16,
		OutputExcerptChars: 8192,
		MaxSteps:           maxToolStepsPerTurn,
	}
	app.loop = agent.NewSelectedLoop(targets, registry, contextPolicy)
	// Taken from the loop rather than the registry so the manifest includes
	// the live MCP catalog and stays "the tools actually available".
	toolNames := app.loop.ToolNames(agent.RunOptions{})
	// Not named repo: this function calls repo.NewWorkspaceSessions and
	// repo.NewStatusTool, and shadowing an imported package inside the one
	// function that uses it most is a trap for the next edit.
	selfRepository := ""
	for _, configured := range config.Repositories {
		if configured.Self {
			selfRepository = configured.Name
			break
		}
	}
	app.manifest = agent.CapabilityManifest{Tools: toolNames, SelfRepository: selfRepository}
	mcpAdministration := newMCPAdmin(app.mcp)
	app.commands = commands.New(commands.Options{
		ConfigPath:   options.ConfigPath,
		MCP:          mcpAdministration.commandsView(),
		Google:       googleAdministration.commandsView(),
		Turns:        app.turns,
		Store:        stateStore,
		Conversation: app.conversation,
		AgentRuntime: app.agentRuntime,
		DefaultModel: config.Agent.DefaultModel,
		ModelAliases: aliases,
	})
	// The turn orchestrator. Bootstrap's remaining job for a turn is to route
	// an event type to the right entry point on this; everything the turn
	// itself does lives in internal/kernel/turns.
	app.turnService = turns.New(turns.Options{
		Commands: app.commands, Registry: app.turns, Conversation: app.conversation,
		Context: contextStore, Store: stateStore, Runtime: app.agentRuntime,
		Skills: app.skillsService, Loop: app.loop, Channel: app.channel,
		Threads: memoryStore, Approvals: app.approvals, Executors: app.approvalExecutors,
		Presenter: turnPresenter{channel: app.channel},
		Manifest:  app.manifest, Logger: app.logger, Now: app.now,
		// The owner's timezone, not the scheduler's quiet-hours one: this is
		// what renders the turn's trusted temporal context.
		Location: app.location, Timezone: timezone,
	})
	app.dispatcher = services.NewDispatcher(owner, stateStore, map[events.Type]services.EventHandler{
		events.TypeMessage: app.processEvent, events.TypeApproval: app.processEvent, events.TypeSchedule: app.processEvent,
		events.TypeScheduledMessage: app.processEvent,
	})
	var webhook http.Handler
	if config.Telegram.Configured() {
		handler := telegram.NewWebhookHandler(config.Telegram.OwnerID, secrets.TelegramWebhookSecret, app.Enqueue, telegramAcknowledger)
		if telegramSelector != nil {
			handler.WithSelectionResolver(telegramSelector.Resolve)
		}
		webhook = handler
	}
	webHandler := web.NewWebHandler(options.ConfigPath, web.WebUIConfig{
		UserEmail: secrets.UIUserEmail, Password: secrets.UIPassword,
		SigningKey: []byte(secrets.EncryptionKey), Now: options.Now,
		ChatHub: app.chatHub, Enqueue: app.Enqueue, Memory: memoryStore, Threads: memoryStore, OwnerID: owner,
		MCP:              mcpAdministration.webView(),
		TrustedProxyHops: config.Server.TrustedProxyHops,
	})
	app.httpHandler = web.NewHTTPHandler(web.Routes{
		Ready: app.Ready, TelegramPath: config.Server.TelegramWebhookPath, Telegram: webhook,
		MCPCallback: mcpCallbackHandler(app.mcp),
		Web:         webHandler,
	})
	if telegramClient != nil {
		autocomplete := commands.TelegramAutocomplete()
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
		sort.Strings(integrations)
		a.logger.Info("agent runtime ready", "model_alias", alias, "provider", provider, "repositories", repositories, "integrations", integrations, "context_files", []string{"SOUL.md", "USER.md", "MEMORY.md"})
	})
	return nil
}

type staticModel struct{}

func (staticModel) Generate(context.Context, ports.ModelRequest) (ports.ModelResponse, error) {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "Eggy fake adapter ready."}}, nil
}

// noopChannel is the channel used when no surface is configured at all. It
// implements only ports.Channel: with nowhere to deliver to there is no
// message to edit in place and no one to show a typing indicator to, so it
// claims neither optional extension rather than stubbing them.
type noopChannel struct{}

func (noopChannel) Deliver(context.Context, string) error                     { return nil }
func (noopChannel) DeliverApproval(context.Context, approvals.Approval) error { return nil }
