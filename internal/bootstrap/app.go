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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/calendar/google"
	"github.com/nigelteosw/eggy/internal/adapters/channels/telegram"
	"github.com/nigelteosw/eggy/internal/adapters/coding/codexcli"
	"github.com/nigelteosw/eggy/internal/adapters/memory/markdown"
	"github.com/nigelteosw/eggy/internal/adapters/models/deepseek"
	githubadapter "github.com/nigelteosw/eggy/internal/adapters/repositories/github"
	"github.com/nigelteosw/eggy/internal/adapters/runner/localprocess"
	schedulerlocal "github.com/nigelteosw/eggy/internal/adapters/scheduler/local"
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
	DeepSeekEndpoint string
	GitHubAPIBase    string
	GoogleAuthURL    string
	GoogleTokenURL   string
	GoogleAPIBase    string
	CodexExecutable  string
	Now              func() time.Time
	FakeAdapters     bool
}

type App struct {
	config       Config
	store        ports.StateStore
	memory       ports.MemoryStore
	channel      ports.Channel
	dispatcher   *services.Dispatcher
	httpHandler  http.Handler
	loop         *agent.Loop
	router       agent.Router
	commands     *CommandService
	scheduler    *schedulerlocal.Scheduler
	heartbeat    *services.HeartbeatPolicy
	approvals    *services.ApprovalService
	coding       *services.CodingService
	shipping     *services.ShippingService
	calendar     *services.CalendarService
	conversation *services.ConversationService
	repositories map[string]ports.Repository
	now          func() time.Time
	eventQueue   chan events.Event
	workers      sync.WaitGroup
}

func NewApp(config Config, secrets Secrets, options AppOptions) (*App, error) {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.TelegramBaseURL == "" {
		options.TelegramBaseURL = "https://api.telegram.org"
	}
	if options.DeepSeekEndpoint == "" {
		options.DeepSeekEndpoint = "https://api.deepseek.com/chat/completions"
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
	if !options.FakeAdapters && len(config.Repositories) > 0 {
		if _, err := exec.LookPath(options.CodexExecutable); err != nil {
			return nil, fmt.Errorf("coding adapter executable %q is unavailable: %w", options.CodexExecutable, err)
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(config.DataDir, "codex"), 0o700); err != nil {
		return nil, err
	}
	stateStore := jsonfile.Open(filepath.Join(config.DataDir, "state.json"))
	memoryStore := markdown.Open(filepath.Join(config.DataDir, "MEMORY.md"))
	app := &App{config: config, store: stateStore, memory: memoryStore, scheduler: schedulerlocal.New(stateStore), repositories: map[string]ports.Repository{}, now: options.Now, eventQueue: make(chan events.Event, 64)}
	for _, configured := range config.Repositories {
		app.repositories[configured.Name] = ports.Repository{Name: configured.Name, CloneURL: configured.CloneURL, BaseBranch: configured.BaseBranch, ProtectedBranches: configured.ProtectedBranches}
	}
	if options.FakeAdapters {
		app.channel = noopChannel{}
	} else {
		app.channel = telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, options.HTTPClient)
	}
	protected := make([]string, 0)
	for _, repository := range app.repositories {
		protected = append(protected, repository.ProtectedBranches...)
	}
	app.approvals = services.NewApprovalService(stateStore, options.Now, 30*time.Minute, protected)
	allowedEnvironment := append([]string(nil), config.Runner.AllowedEnv...)
	allowedEnvironment = append(allowedEnvironment, "CODEX_HOME", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT")
	runner, err := localprocess.New(config.Runner.Root, allowedEnvironment, config.Runner.Timeout.Value(), config.Runner.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	repositoryAdapter := githubadapter.New(runner, secrets.GitHubToken, options.GitHubAPIBase, options.HTTPClient)
	codex := codexcli.New(options.CodexExecutable, runner, config.Runner.MaxOutputBytes)
	app.coding = services.NewCodingService(stateStore, runner, repositoryAdapter, codex, filepath.Join(config.DataDir, "codex"), options.Now)
	app.shipping = services.NewShippingService(stateStore, app.approvals, repositoryAdapter, app.repositories)
	app.shipping.SetApprovalRequester(app.approvals)
	app.conversation = services.NewConversationService(stateStore, 20)

	var flash, pro ports.Model
	if options.FakeAdapters {
		flash, pro = staticModel{}, staticModel{}
	} else {
		model := deepseek.New(options.DeepSeekEndpoint, secrets.DeepSeekAPIKey, options.HTTPClient)
		flash, pro = model, model
	}
	registry := services.NewToolRegistry()
	for _, tool := range []ports.Tool{services.NewStatusTool(stateStore), services.NewMemoryLoadTool(memoryStore), services.NewMemoryAppendTool(memoryStore), services.NewMemoryReplaceTool(memoryStore)} {
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
		key, err := base64.StdEncoding.DecodeString(secrets.EncryptionKey)
		if err != nil {
			return nil, err
		}
		googleStart, googleCallback = google.NewOAuthHandlers(googleAdapter, stateStore, key, options.Now)
		for _, tool := range calendarTools(app.calendar, app.channel, strconv.FormatInt(config.Telegram.OwnerID, 10), config.Calendar.DefaultCalendar) {
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
	app.loop = agent.NewLoop(flash, pro, registry.Tools(), agent.Config{FlashModel: config.Models.Flash.ID, ProModel: config.Models.Pro.ID, MaxToolSteps: 8, EscalateAfterSteps: config.Models.Escalation.ToolSteps, EscalateAfterFailures: config.Models.Escalation.RecoverableFailures})
	repositoryNames := make([]string, 0, len(app.repositories))
	for name := range app.repositories {
		repositoryNames = append(repositoryNames, name)
	}
	app.router = agent.Router{Repositories: repositoryNames, ComplexityLength: 600}
	location, err := time.LoadLocation(config.Scheduler.QuietHours.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load scheduler timezone: %w", err)
	}
	app.heartbeat, err = services.NewHeartbeatPolicy(config.Scheduler.QuietHours.Start, config.Scheduler.QuietHours.End, location, config.Scheduler.MinimumProactiveInterval.Value(), config.Scheduler.WeeklyProactiveLimit)
	if err != nil {
		return nil, err
	}
	app.commands = &CommandService{config: config, store: stateStore, memory: memoryStore, conversation: app.conversation, coding: app.coding, now: options.Now}
	owner := strconv.FormatInt(config.Telegram.OwnerID, 10)
	app.dispatcher = services.NewDispatcher(owner, stateStore, map[events.Type]services.EventHandler{
		events.TypeMessage: app.processEvent, events.TypeApproval: app.processEvent, events.TypeSchedule: app.processEvent, events.TypeHeartbeat: app.processEvent,
	})
	webhook := telegram.NewWebhookHandler(config.Telegram.OwnerID, secrets.TelegramWebhookSecret, app.Enqueue)
	app.httpHandler = NewHTTPHandlerAt(config.Server.TelegramWebhookPath, app.Ready, webhook, googleStart, googleCallback)
	return app, nil
}

func (a *App) Handler() http.Handler { return a.httpHandler }
func (a *App) ExecuteCommand(ctx context.Context, command string) (string, bool, error) {
	return a.commands.Execute(ctx, command)
}
func (a *App) Ready() error {
	if _, err := a.store.Load(context.Background()); err != nil {
		return err
	}
	if _, err := a.memory.Load(context.Background()); err != nil {
		return err
	}
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
		return a.handleMessage(ctx, message)
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

func (a *App) handleMessage(ctx context.Context, message events.Message) error {
	if output, handled, err := a.commands.Execute(ctx, message.Text); handled {
		if err != nil {
			return err
		}
		return a.channel.Deliver(ctx, message.ChatID, output)
	}
	if repositoryName, coding := a.router.CodingIntent(message.Text); coding && repositoryName != "" {
		repository := a.repositories[repositoryName]
		runID := newRunID()
		if err := a.channel.Deliver(ctx, message.ChatID, "Started coding run "+runID+" in "+repositoryName+"."); err != nil {
			return err
		}
		run, result, err := a.coding.Start(ctx, runID, repository, message.Text, func(progress ports.CodingProgress) {
			if progress.Message != "" {
				_ = a.channel.Deliver(context.Background(), message.ChatID, progress.Message)
			}
		})
		if err != nil {
			_ = a.channel.Deliver(ctx, message.ChatID, "Coding run failed: "+err.Error())
			return err
		}
		if err := a.channel.Deliver(ctx, message.ChatID, result.Summary+"\n\nValidation:\n"+result.Validation); err != nil {
			return err
		}
		approval, err := a.shipping.RequestCommit(ctx, run.ID, result.CommitMessage)
		if err != nil {
			return err
		}
		return a.channel.DeliverApproval(ctx, message.ChatID, approval)
	}
	memory, err := a.memory.Load(ctx)
	if err != nil {
		return err
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	history := []ports.Message{{Role: ports.RoleSystem, Content: "You are Eggy, a personal assistant. Current instructions override memory.\n\nDurable memory:\n" + memory}}
	if state.ConversationSummary != "" {
		history = append(history, ports.Message{Role: ports.RoleSystem, Content: "Conversation summary:\n" + state.ConversationSummary})
	}
	history = append(history, state.RecentMessages...)
	forcePro := strings.Contains(strings.ToLower(message.Text), "use pro") || a.router.ComplexNonCoding(message.Text)
	response, err := a.loop.Run(ctx, message.Text, history, forcePro)
	if err != nil {
		return err
	}
	if err := a.conversation.Record(ctx, ports.Message{Role: ports.RoleUser, Content: message.Text}); err != nil {
		return err
	}
	if err := a.conversation.Record(ctx, response); err != nil {
		return err
	}
	return a.channel.Deliver(ctx, message.ChatID, response.Content)
}

func (a *App) handleApproval(ctx context.Context, decision events.ApprovalDecision) error {
	chatID := strconv.FormatInt(a.config.Telegram.OwnerID, 10)
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
		return a.channel.Deliver(ctx, chatID, "Action rejected.")
	}
	state, err := a.store.Load(ctx)
	if err != nil {
		return err
	}
	approval := state.Approvals[decision.ApprovalID]
	var result any
	switch approval.Action {
	case approvals.Commit, approvals.Push, approvals.CreatePR:
		result, err = a.shipping.ExecuteApproved(ctx, approval)
	case approvals.CalendarCreate, approvals.CalendarUpdate, approvals.CalendarDelete:
		if a.calendar == nil {
			return errors.New("Calendar is unavailable")
		}
		result, err = a.calendar.ExecuteApproved(ctx, approval)
	default:
		return errors.New("unknown approval action")
	}
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
			return err
		}
		if err := a.channel.Deliver(ctx, chatID, fmt.Sprintf("Committed %v.", result)); err != nil {
			return err
		}
		return a.channel.DeliverApproval(ctx, chatID, next)
	}
	if approval.Action == approvals.Push {
		var payload struct{ RunID, Branch string }
		_ = json.Unmarshal(approval.Payload, &payload)
		next, err := a.shipping.RequestPullRequest(ctx, payload.RunID, payload.Branch, "Eggy: "+payload.Branch, "Automated by Eggy after explicit owner approvals.")
		if err != nil {
			return err
		}
		if err := a.channel.Deliver(ctx, chatID, "Push completed."); err != nil {
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
	return a.channel.Deliver(ctx, chatID, fmt.Sprintf("Approved action completed: %v", result))
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
	memory, err := a.memory.Load(ctx)
	if err != nil {
		return err
	}
	response, err := a.loop.Run(ctx, "Evaluate whether one concise proactive check-in is useful now. Return an empty response when none is useful.", []ports.Message{{Role: ports.RoleSystem, Content: "Heartbeat context only. Protected writes are forbidden.\n\n" + memory}}, false)
	if err != nil || strings.TrimSpace(response.Content) == "" {
		return err
	}
	if err := a.heartbeat.Record(ctx, a.store, a.now()); err != nil {
		return err
	}
	return a.channel.Deliver(ctx, strconv.FormatInt(a.config.Telegram.OwnerID, 10), response.Content)
}

func newRunID() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

type staticModel struct{}

func (staticModel) Generate(context.Context, ports.ModelRequest) (ports.ModelResponse, error) {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "Eggy fake adapter ready."}}, nil
}

type noopChannel struct{}

func (noopChannel) Deliver(context.Context, string, string) error                     { return nil }
func (noopChannel) DeliverApproval(context.Context, string, approvals.Approval) error { return nil }
