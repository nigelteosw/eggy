package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
	contextmarkdown "github.com/nigelteosw/eggy/plugins/context/markdown"
	memorysqlite "github.com/nigelteosw/eggy/plugins/memory/sqlite"
	"github.com/nigelteosw/eggy/plugins/models/openaicompat"
	"github.com/nigelteosw/eggy/plugins/scheduler/cronfile"
	"github.com/nigelteosw/eggy/plugins/state/jsonfile"
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

// stores is every durable artifact NewApp opens, already migrated.
type stores struct {
	layout  home.Layout
	state   ports.StateStore
	cron    *cronfile.Store
	context ports.ContextStore
	memory  *memorysqlite.Store
}

// openStores resolves the home layout and opens every store off it. The caller
// owns closing stores.memory: this returns it open on success.
func openStores(config config.Config) (stores, error) {
	// config.DataDir is the home root: every durable artifact resolves off
	// this one layout instead of a path literal spread across the wiring.
	// Migrate first, so a home written by an older Eggy is current before
	// any store opens a file in it.
	layout := home.At(config.DataDir)
	if err := layout.Migrate(); err != nil {
		return stores{}, err
	}
	opened := stores{
		layout: layout,
		cron:   cronfile.Open(layout.Cron()),
	}
	opened.state = jsonfile.Open(layout.State())
	opened.context = contextmarkdown.Open(contextmarkdown.Paths{
		Soul: layout.Soul(), User: layout.User(), Memory: layout.Memory(), Watch: layout.Watch(),
	}, contextmarkdown.DefaultUserMaxBytes, contextmarkdown.DefaultMemoryMaxBytes, contextmarkdown.DefaultWatchMaxBytes)
	memoryStore, err := memorysqlite.Open(layout.Database())
	if err != nil {
		return stores{}, fmt.Errorf("open conversation memory: %w", err)
	}
	opened.memory = memoryStore
	return opened, nil
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
		catalog.targets[alias] = agent.ModelTarget{Model: model, ModelID: configured.Model}
		if len(configured.ReasoningEfforts) > 0 {
			catalog.efforts[alias] = configured.ReasoningEfforts
		}
	}
	slices.Sort(catalog.aliases)
	return catalog, nil
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
