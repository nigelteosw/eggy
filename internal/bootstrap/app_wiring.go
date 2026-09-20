package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/auth/grants"
	contextmarkdown "github.com/nigelteosw/eggy/plugins/context/markdown"
	"github.com/nigelteosw/eggy/plugins/models/openaicompat"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// This file holds the parts of NewApp's wiring that are self-contained enough
// to name: option defaults, the durable stores, and the model catalog. Each
// takes what it needs and returns what it built, so NewApp reads as a sequence
// of steps rather than one long straight line.

func (o *AppOptions) applyDefaults() {
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.TelegramBaseURL == "" {
		o.TelegramBaseURL = "https://api.telegram.org"
	}
	if o.GitHubAPIBase == "" {
		o.GitHubAPIBase = "https://api.github.com"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

func initializeAccountState(stateStore ports.StateStore, repositories map[string]ports.Repository, accountID string) error {
	ctx := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: accountID})
	initial, err := stateStore.Load(ctx)
	if err != nil {
		return err
	}
	if _, err := stateStore.Update(ctx, initial.Version, func(state *ports.State) error {
		state.Repositories = repositories
		return nil
	}); err != nil {
		return fmt.Errorf("sync configured repositories: %w", err)
	}
	return nil
}

// stores is every durable artifact NewApp opens, already migrated. Only two
// things are actually opened: the owner's Markdown, and the one database that
// is the authority for everything machine-managed. State, schedules, and
// sealed grants are views onto that database rather than stores of their own.
type stores struct {
	layout    home.Layout
	state     ports.StateStore
	schedules *sqlitestore.ScheduleStore
	auth      grants.Records
	context   ports.ContextStore
	database  *sqlitestore.Store
}

// openStores resolves the home layout, opens the database, and migrates a
// home written before machine state consolidated into it. The caller owns
// closing stores.database: this returns it open on success.
func openStores(config config.Config, logger *slog.Logger) (stores, error) {
	// config.DataDir is the home root: every durable artifact resolves off
	// this one layout instead of a path literal spread across the wiring.
	// Migrate first, so a home written by an older Eggy is current before
	// any store opens a file in it.
	layout := home.At(config.DataDir)
	if err := layout.Migrate(); err != nil {
		return stores{}, err
	}
	opened := stores{layout: layout}
	// SOUL.md is shared; the private documents resolve per account from the
	// principal on each call, through the one layout function that
	// validates the ID before it becomes a path.
	opened.context = contextmarkdown.Open(contextmarkdown.Paths{
		Soul: layout.Soul(), Memories: layout.AccountMemories,
	}, contextmarkdown.DefaultUserMaxBytes, contextmarkdown.DefaultMemoryMaxBytes, contextmarkdown.DefaultWatchMaxBytes)
	database, err := sqlitestore.Open(layout.Database())
	if err != nil {
		return stores{}, fmt.Errorf("open eggy database: %w", err)
	}
	// The import runs before any of the views below is read, so a home
	// upgraded by this boot is already current when the first Load happens.
	// It is idempotent and silent on a home that has nothing left to move.
	report, err := database.ImportLegacy(context.Background(), sqlitestore.LegacyHome{
		State: layout.LegacyState(), Cron: layout.LegacyCron(), Auth: layout.LegacyAuth(),
	}, nil)
	if err != nil {
		_ = database.Close()
		return stores{}, err
	}
	if report.Any() {
		logger.Info("migrated home into sqlite", "moved", report.Moved)
	}
	// Records written before accounts existed -- including whatever the
	// import above just moved -- are unowned until the boot names their
	// account. A legacy owner is that account on every boot; an explicit
	// accounts list has to say so through migration_owner_id, because the
	// list's order does not decide whose history this was.
	if err := assignLegacyRecords(context.Background(), database, layout, config); err != nil {
		_ = database.Close()
		return stores{}, err
	}
	// The documents move to the same account, with the database recording
	// each phase. Both halves finish here, before any store is handed out,
	// so no turn runs over a half-moved home.
	if owner := legacyOwner(config); owner != "" {
		if err := layout.MigrateAccountDocuments(owner, database); err != nil {
			_ = database.Close()
			return stores{}, fmt.Errorf("move owner documents to account %q: %w", owner, err)
		}
	}
	// Credential rows follow membership: configured accounts get one,
	// removed ones are retired. Before any store is handed out, so the
	// first login of this boot finds its row.
	if err := reconcileAccountAuth(context.Background(), database, config); err != nil {
		_ = database.Close()
		return stores{}, err
	}
	opened.database = database
	opened.state = database.State()
	opened.schedules = database.Schedules()
	opened.auth = database.Auth()
	return opened, nil
}

// assignLegacyRecords maps unowned private records to the account config
// names for them, or refuses to boot when there are such records and no
// name. Conversion to accounts also invalidates pending legacy approvals: they
// were requested before there was an account to bind them to.
func assignLegacyRecords(ctx context.Context, database *sqlitestore.Store, layout home.Layout, config config.Config) error {
	owner := legacyOwner(config)
	recorded, err := database.LegacyAccount(ctx)
	if err != nil {
		return err
	}
	if owner == "" {
		if database.HasUnownedRecords(ctx) || (recorded != "" && !accountConfigured(config, recorded)) {
			return fmt.Errorf("this home holds history from before accounts were configured; set migration_owner_id to the account that should receive it")
		}
		return nil
	}
	// The recorded owner is the single owner's old name -- a Telegram number
	// or owner.id -- and moves to the migration owner on conversion. A
	// recorded owner that is a live account in its own right is a different
	// person, and moving their history would be a silent transfer.
	if recorded != "" && recorded != owner && accountConfigured(config, recorded) {
		return fmt.Errorf("legacy records were already assigned to account %q; they cannot be reassigned to %q", recorded, owner)
	}
	if err := database.MigrateAccounts(ctx, owner, config.AccountMode()); err != nil {
		return fmt.Errorf("assign legacy records: %w", err)
	}
	if recorded != "" && recorded != owner {
		if err := layout.RenameAccount(recorded, owner); err != nil {
			return fmt.Errorf("move %q's documents to %q: %w", recorded, owner, err)
		}
	}
	return nil
}

func accountConfigured(config config.Config, id string) bool {
	_, ok := config.Account(id)
	return ok
}

// legacyOwner is the account that receives everything written before
// accounts existed: the single owner in the legacy shape, or the account
// migration_owner_id names. Empty means nobody has said, and a home with
// legacy records refuses to boot until somebody does.
func legacyOwner(config config.Config) string {
	if config.AccountMode() {
		return config.MigrationOwnerID
	}
	return config.Owner.ID
}

// modelCatalog is the configured provider set resolved into what the agent
// loop and the runtime each need.
type modelCatalog struct {
	providers map[string]ports.Model
	aliases   []string
	targets   map[string]agent.ModelTarget
	efforts   map[string][]string
}

func buildModelCatalog(config config.Config, secrets config.Secrets, options AppOptions) (modelCatalog, error) {
	catalog := modelCatalog{
		providers: make(map[string]ports.Model, len(config.Providers)),
		aliases:   make([]string, 0, len(config.ModelAliases)),
		targets:   make(map[string]agent.ModelTarget, len(config.ModelAliases)),
		efforts:   make(map[string][]string, len(config.ModelAliases)),
	}
	for name, provider := range config.Providers {
		model, err := newModelAdapter(name, provider, config, secrets, options)
		if err != nil {
			return modelCatalog{}, err
		}
		catalog.providers[name] = model
	}
	for alias, configured := range config.ModelAliases {
		model := catalog.providers[configured.Provider]
		if model == nil {
			return modelCatalog{}, fmt.Errorf("model alias %q provider %q is unavailable", alias, configured.Provider)
		}
		catalog.aliases = append(catalog.aliases, alias)
		target := agent.ModelTarget{Model: model, ModelID: configured.Model}
		if configured.OpenRouter != nil {
			// Marshalled once here into OpenRouter's own `provider` object;
			// the adapter forwards the bytes and the kernel never reads them.
			routing, err := json.Marshal(openRouterRoutingBody(*configured.OpenRouter))
			if err != nil {
				return modelCatalog{}, fmt.Errorf("model alias %q openrouter routing: %w", alias, err)
			}
			target.ProviderRouting = routing
		}
		catalog.targets[alias] = target
		if len(configured.ReasoningEfforts) > 0 {
			catalog.efforts[alias] = configured.ReasoningEfforts
		}
	}
	slices.Sort(catalog.aliases)
	return catalog, nil
}

// modelDiscovery answers "what does this provider say it serves?" for the
// settings panel. It is built from the same adapters the turn path runs on, so
// a browse listing is authenticated by the provider's own configured key and
// no second credential path exists.
//
// A provider appears here only if it opted in (discover_models, on by default)
// and its adapter can actually list. Everything else is simply absent, which
// the panel renders as "this provider cannot be browsed" -- not an error,
// because a provider with no catalog is a normal, working provider.
type modelDiscovery struct {
	providers map[string]ports.ModelCatalog
}

func newModelDiscovery(cfg config.Config, adapters map[string]ports.Model) *modelDiscovery {
	discovery := &modelDiscovery{providers: map[string]ports.ModelCatalog{}}
	for name, provider := range cfg.Providers {
		if !provider.DiscoversModels() {
			continue
		}
		if listable, ok := adapters[name].(ports.ModelCatalog); ok {
			discovery.providers[name] = listable
		}
	}
	return discovery
}

func (d *modelDiscovery) DiscoverableProviders() []string {
	names := slices.Sorted(maps.Keys(d.providers))
	return names
}

func (d *modelDiscovery) DiscoverModels(ctx context.Context, provider string) ([]ports.CatalogModel, error) {
	listable, ok := d.providers[provider]
	if !ok {
		return nil, fmt.Errorf("provider %q cannot list models", provider)
	}
	return listable.ListModels(ctx)
}

// trace wraps every resolved target's model in the recorder, in place. It is
// applied to the catalog rather than inside newModelAdapter so that a new
// adapter case cannot be added without tracing: the wrapping happens after
// the switch, to whatever the switch returned.
func (c *modelCatalog) trace(recorder *services.TraceRecorder) {
	if recorder == nil {
		return
	}
	// Only the targets are wrapped: they are what the loop runs on, and
	// providers is the intermediate map they were resolved from. Wrapping
	// both would put two recorders on one call path.
	for alias, target := range c.targets {
		target.Model = services.NewTracedModel(target.Model, recorder)
		c.targets[alias] = target
	}
}

// newModelAdapter is the one place a provider's configured adapter name
// becomes a running implementation. This is the extension point for a new
// model backend: add the plugin package, add its name to
// config.supportedModelAdapters so the name validates, and add one case here.
// Nothing else in the tree needs to change.
//
// Most backends need none of that. "openai_compatible" is OpenAI's Chat
// Completions wire format, so OpenAI, DeepSeek, OpenRouter, and Groq are all
// reachable by adding a providers entry with the right base_url and
// api_key_env. A new case is earned by a different wire format, not a
// different vendor.
func newModelAdapter(name string, provider config.ProviderConfig, cfg config.Config, secrets config.Secrets, options AppOptions) (ports.Model, error) {
	// Checked before the adapter name so a fake-adapter run never dials out,
	// whatever the config happens to say.
	if options.FakeAdapters {
		return staticModel{}, nil
	}
	switch provider.Adapter {
	case "openai_compatible":
		return openaicompat.New(providerBaseURL(cfg, options, name), secrets.ProviderAPIKeys[name], options.HTTPClient), nil
	default:
		return nil, fmt.Errorf("provider %q has unsupported adapter %q", name, provider.Adapter)
	}
}

// providerBaseURL is the configured base URL for a provider, with the test
// override applied.
func providerBaseURL(config config.Config, options AppOptions, provider string) string {
	if override := options.ProviderBaseURLs[provider]; override != "" {
		return override
	}
	return config.Providers[provider].BaseURL
}

// registerAll registers every tool or fails on the first rejection. The
// registry rejects duplicates, so a colliding adapter fails bootstrap rather
// than silently winning.
func registerAll(registry *services.ToolRegistry, tools ...ports.Tool) error {
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// gateAll wraps every native tool in the approval gate, reads included.
//
// Every one, because the mode is durable runtime state that changes without a
// restart: if only the tools that mutate carried a gate, switching to strict
// would gate nothing new until the next boot, which is the wrong direction for
// a setting whose whole purpose is to tighten things now. The wrapper decides
// per call instead, and in normal mode a read-only tool's rule passes it
// straight through at the cost of one map lookup.
//
// MCP is excluded and keeps its own path: a remote catalog cannot be
// classified from here, and its trust decision was made when the server was
// configured.
func registerGated(registry *services.ToolRegistry, asker *approvalAsker, modes services.ModeReader, tools ...ports.Tool) error {
	gated := make([]ports.Tool, 0, len(tools))
	for _, tool := range tools {
		gated = append(gated, services.NewApprovalGatedToolIf(tool, asker, modes, services.RuleFor(tool.Definition())))
	}
	return registerAll(registry, gated...)
}

// approvalAsker records the approval and then actually asks the owner.
//
// Recording and asking are two steps and only the first lived in the kernel:
// ApprovalService writes the pending record, and the channel carries the
// question with its approve and reject buttons. Joining them here rather than
// inside the service is what keeps `internal/kernel` free of a channel, the
// same reason googleAdmin exists.
//
// Nothing joined them for a while. The last caller of DeliverApproval went out
// with the native Calendar tools, which left the gate writing pending records
// nobody was ever shown -- the model was told "the owner has been asked" and
// the owner was asked nothing. A gate that silently swallows the question is
// worse than no gate: the call does not happen, and nobody knows why.
type approvalAsker struct {
	service *services.ApprovalService
	channel ports.Channel
}

func (a *approvalAsker) Request(ctx context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	// Recorded first, so a delivery failure leaves an approval the owner can
	// still find in /status and the web panel rather than losing it.
	approval, err := a.service.Request(ctx, action, payload, summary)
	if err != nil {
		return approvals.Approval{}, err
	}
	if a.channel == nil {
		return approval, nil
	}
	if err := a.channel.DeliverApproval(ctx, approval); err != nil {
		// Reported rather than swallowed. The model must not tell the owner
		// their approval is waiting on a message that never arrived.
		return approvals.Approval{}, fmt.Errorf("could not ask the owner to approve: %w", err)
	}
	return approval, nil
}

// openRouterRoutingBody is OpenRouter's `provider` request object, spelled
// with its wire names. Only set fields are sent so OpenRouter's defaults
// apply to the rest.
func openRouterRoutingBody(routing config.OpenRouterRoutingConfig) map[string]any {
	body := map[string]any{}
	if len(routing.Order) > 0 {
		body["order"] = routing.Order
	}
	if len(routing.Only) > 0 {
		body["only"] = routing.Only
	}
	if len(routing.Ignore) > 0 {
		body["ignore"] = routing.Ignore
	}
	if routing.AllowFallbacks != nil {
		body["allow_fallbacks"] = *routing.AllowFallbacks
	}
	if routing.Sort != "" {
		body["sort"] = routing.Sort
	}
	return body
}
