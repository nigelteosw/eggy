package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Value() time.Duration { return time.Duration(d) }

func (d Duration) MarshalYAML() (any, error) { return d.Value().String(), nil }

// Config is both the parsed shape and the written shape. It carries its own
// yaml tags rather than being projected through a separate document type:
// ModelAliases is the only field whose Go name and YAML key differ, and a tag
// says that in one line.
type Config struct {
	Server       ServerConfig                `yaml:"server"`
	DataDir      string                      `yaml:"data_dir"`
	Owner        OwnerConfig                 `yaml:"owner"`
	Telegram     TelegramConfig              `yaml:"telegram,omitempty"`
	Agent        AgentConfig                 `yaml:"agent"`
	Providers    map[string]ProviderConfig   `yaml:"providers"`
	ModelAliases map[string]ModelAliasConfig `yaml:"models"`
	Repositories []RepositoryConfig          `yaml:"repositories"`
	Runner       RunnerConfig                `yaml:"runner"`
	MCP          MCPConfig                   `yaml:"mcp,omitempty"`
	Google       GoogleConfig                `yaml:"google,omitempty"`
	Tavily       TavilyConfig                `yaml:"tavily,omitempty"`
	Heartbeat    HeartbeatConfig             `yaml:"heartbeat,omitempty"`
	Appearance   AppearanceConfig            `yaml:"appearance,omitempty"`
	Approvals    ApprovalsConfig             `yaml:"approvals,omitempty"`
	Tracing      TracingConfig               `yaml:"tracing,omitempty"`
}

// ApprovalsConfig sets where a deployment starts. It is only a default: once
// the owner has chosen with /mode, that choice is durable and config no longer
// speaks for it. A deployment is where the software begins, not a standing
// instruction that outranks the person operating it.
// TracingConfig governs the turn trace: every model call with the prompt that
// produced it, every tool call with its arguments and output, kept so the
// owner can see what Eggy actually did rather than only what it replied.
//
// It is on unless switched off, which is the one place this section departs
// from "a capability that is not configured costs nothing at runtime". The
// rule is about cost, and the cost here is bounded by keep_turns and
// retention; a trace nobody switched on is a trace that is missing exactly
// when it is wanted, because the thing worth tracing has already happened.
type TracingConfig struct {
	// Enabled is a pointer so that absent and false are distinguishable: the
	// default is on, and a plain bool would make every config written before
	// this section existed silently opt out.
	Enabled *bool `yaml:"enabled,omitempty"`
	// KeepTurns is how many traces are retained regardless of age. Full
	// prompt bodies are the largest thing Eggy writes, so this is the ceiling
	// that makes recording them affordable.
	KeepTurns int `yaml:"keep_turns,omitempty"`
	// Retention drops traces older than this even when the count is under
	// KeepTurns. A prompt is the most sensitive document Eggy holds -- it
	// carries USER.md, MEMORY.md and the recent conversation -- so it should
	// not sit on disk indefinitely for the sake of a turn nobody will revisit.
	Retention Duration `yaml:"retention,omitempty"`
	// MaxBodyBytes caps one recorded prompt or tool output. A safety valve
	// rather than a budget: a tool returning a hundred megabytes must not be
	// able to take the database with it.
	MaxBodyBytes int64 `yaml:"max_body_bytes,omitempty"`
}

// Active reports whether traces are recorded. Absent means on.
func (t TracingConfig) Active() bool { return t.Enabled == nil || *t.Enabled }

type ApprovalsConfig struct {
	// Mode is strict, normal or auto. Empty means normal.
	Mode string `yaml:"mode,omitempty"`
}

// Theme names. Dark is the default: Eggy's web panel is charcoal unless the
// owner says otherwise, so an absent appearance section needs no migration.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// AppearanceConfig is the owner's web panel preference. It lives in YAML
// rather than in the browser because the owner is one person across several
// devices: a preference kept in localStorage is a preference they set again
// on every laptop they log in from.
//
// It is the one config section that changes nothing at runtime -- no adapter
// reads it, no tool schema depends on it -- so unlike every other section it
// takes effect on the next page load rather than on restart.
type AppearanceConfig struct {
	Theme string `yaml:"theme,omitempty"`
}

// ResolvedTheme is Theme with the default applied, so callers never have to
// distinguish "unset" from "dark".
func (a AppearanceConfig) ResolvedTheme() string {
	if a.Theme == ThemeLight {
		return ThemeLight
	}
	return ThemeDark
}

// HeartbeatConfig is the periodic check-in the owner is not present for. An
// absent section or a zero interval means off, and off costs nothing at
// runtime: no ticker, no goroutine, no model call. Instruction overrides the
// built-in prompt; the silence protocol lives in the system message rather
// than here, so overriding this cannot delete it.
type HeartbeatConfig struct {
	Interval    Duration `yaml:"interval,omitempty"`
	Instruction string   `yaml:"instruction,omitempty"`
	// ActiveHours confines beats to a window of the day. An interval-only
	// heartbeat fires at 03:00, and muting the chat to survive that disables
	// the feature.
	ActiveHours ActiveHours `yaml:"active_hours,omitempty"`
	// IncludeRecentHistory lets a beat see the recent conversation window, so
	// it can notice that the owner said they would ship something on Friday.
	//
	// Off by default, deliberately. Unprompted turns carry no ambient history
	// so an owner's earlier chat cannot silently steer a turn they are not
	// present for and did not review at fire time -- a standing invariant
	// recorded in AGENTS.md and at turns.ScheduledTurn. Defaulting to false
	// keeps that true for anyone who does not opt in, and makes the
	// relaxation one auditable line in config.yaml. Tools stay read-only
	// either way: this changes what a beat knows, never what it can do.
	IncludeRecentHistory bool `yaml:"include_recent_history,omitempty"`
}

// ActiveHours is a window of the day, Start inclusive and End exclusive, read
// in the owner's configured timezone (agent.timezone) rather than a zone of
// its own.
//
// Either bound empty means always active, so an absent section changes
// nothing. A window whose End is before its Start wraps midnight, which is how
// an overnight watch is written.
type ActiveHours struct {
	Start string `yaml:"start,omitempty"`
	End   string `yaml:"end,omitempty"`
}

// Configured reports whether both bounds are set. One bound alone is a
// mistake rather than a half-window, and Validate rejects it.
func (h ActiveHours) Configured() bool {
	return strings.TrimSpace(h.Start) != "" && strings.TrimSpace(h.End) != ""
}

// Active reports whether when falls inside the window. An unconfigured or
// unparseable window is always active: the parse already passed Validate at
// load, so failing open here means a bug suppresses nothing silently.
func (h ActiveHours) Active(when time.Time) bool {
	if !h.Configured() {
		return true
	}
	start, err := parseClock(h.Start)
	if err != nil {
		return true
	}
	end, err := parseClock(h.End)
	if err != nil {
		return true
	}
	minutes := when.Hour()*60 + when.Minute()
	if start < end {
		return minutes >= start && minutes < end
	}
	// Wrapped across midnight: inside means after the start or before the end.
	return minutes >= start || minutes < end
}

// NextOpen reports how long after when the window is next active, and whether
// there is a window at all.
//
// It exists so a beat scheduled into quiet hours can be moved to the window
// opening instead of being dropped. Dropping it is what a fixed ticker does,
// and it costs the owner up to a whole interval every morning: a wake computed
// for 07:00 against an 08:00 window is skipped, and the next one does not
// arrive until 10:00.
//
// Zero means when is already inside the window, so a caller can add the result
// unconditionally. The bool distinguishes that from an unconfigured window,
// where there is nothing to move to.
//
// The opening is built with time.Date in when's own location rather than by
// adding 24h, so a window that crosses a daylight-saving change opens at the
// wall-clock time the owner wrote rather than an hour either side of it.
func (h ActiveHours) NextOpen(when time.Time) (time.Duration, bool) {
	if !h.Configured() {
		return 0, false
	}
	start, err := parseClock(h.Start)
	if err != nil {
		// Unparseable fails open, as Active does and for the same reason: the
		// parse already passed Validate at load, so a bug here must not
		// silently defer every beat to a window it cannot read.
		return 0, false
	}
	if h.Active(when) {
		return 0, true
	}
	open := time.Date(when.Year(), when.Month(), when.Day(), start/60, start%60, 0, 0, when.Location())
	if !open.After(when) {
		// Today's opening has passed, so the next one is tomorrow's. Day+1
		// is normalized by time.Date across month and year ends.
		open = time.Date(when.Year(), when.Month(), when.Day()+1, start/60, start%60, 0, 0, when.Location())
	}
	return open.Sub(when), true
}

// parseClock reads an HH:MM clock time as minutes past midnight. 24:00 is
// allowed, so an end bound can name midnight unambiguously.
func parseClock(value string) (int, error) {
	var hour, minute int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d:%d", &hour, &minute); err != nil {
		return 0, fmt.Errorf("%q is not HH:MM", value)
	}
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 || (hour == 24 && minute != 0) {
		return 0, fmt.Errorf("%q is not a time of day", value)
	}
	return hour*60 + minute, nil
}

// GoogleConfig is one grant across every Google product, which is why there is
// no per-product block here. The client is a Desktop (installed app) client:
// Google grants those an implicit loopback redirect, so authorization needs
// nothing registered in the console and no publicly reachable address, and
// server.public_base_url plays no part in it.
type GoogleConfig struct {
	Enabled bool `yaml:"enabled"`
	// ClientID is not a secret -- it travels in the authorization URL -- so it
	// lives in YAML. ClientSecretEnv names the variable holding the secret,
	// which never does. A Desktop client's secret is not confidential in the
	// OAuth sense, but it is still a credential and is treated as one.
	ClientID        string `yaml:"client_id,omitempty"`
	ClientSecretEnv string `yaml:"client_secret_env,omitempty"`
	// Products decides which tools exist at all. An unlisted product costs no
	// schema, no prompt bytes, and no code path -- the same rule every other
	// configurable capability follows.
	Products []string `yaml:"products,omitempty"`
	Scopes   []string `yaml:"scopes,omitempty"`
	// RequireApproval names the actions that must ask the owner before they
	// run, as product.action -- "gmail.send", "calendar.delete". Omitted, every
	// action that writes anything is gated; set to an empty list, none are.
	// Distinguishing the two is the whole point of the field, so it must stay
	// out of any defaulting that would fill a nil in.
	//
	// It is a pointer because omitempty cannot tell an empty list from an
	// absent one and would drop both -- which would turn "ask me about
	// nothing" back into "ask me about everything that writes" the first time
	// a surface rewrote the file.
	RequireApproval *[]string `yaml:"require_approval,omitempty"`
	Timeout         Duration  `yaml:"timeout,omitempty"`
	MaxOutputBytes  int64     `yaml:"max_output_bytes,omitempty"`
}

type AgentConfig struct {
	DefaultModel string `yaml:"default_model"`
	Timezone     string `yaml:"timezone,omitempty"`
}

type ProviderConfig struct {
	Adapter   string `yaml:"adapter"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	// DiscoverModels allows a surface to ask the provider what it serves,
	// through the adapter's /models listing. It is a pointer so that absent
	// and false are distinguishable: the default is on, and a plain bool
	// would silently opt every config written before this field out of it.
	//
	// Discovery is a browse list, never an allowlist. What Eggy will actually
	// run stays exactly what ModelAliases names -- a discovered model becomes
	// selectable only once an owner has written it down as an alias. That
	// separation is the whole point: the catalog is provider-controlled and
	// changes without warning, so it may inform a choice but must not be one.
	DiscoverModels *bool `yaml:"discover_models,omitempty"`
}

// DiscoversModels reports whether this provider may be asked for its catalog.
// Absent means yes, which is what makes the field an opt-out rather than a
// setting every existing provider entry has to learn about.
func (p ProviderConfig) DiscoversModels() bool { return p.DiscoverModels == nil || *p.DiscoverModels }

type ModelAliasConfig struct {
	Provider         string   `yaml:"provider"`
	Model            string   `yaml:"model"`
	ReasoningEfforts []string `yaml:"reasoning_efforts,omitempty"`
}

type ServerConfig struct {
	Listen              string `yaml:"listen"`
	PublicBaseURL       string `yaml:"public_base_url"`
	TelegramWebhookPath string `yaml:"telegram_webhook_path"`
	// TrustedProxyHops is how many reverse proxies sit in front of Eggy: 0
	// when it is exposed directly, 1 behind a single proxy such as
	// Railway's. The web login throttle uses it to find the real client
	// address in X-Forwarded-For; at 0 the header is ignored entirely.
	// Stating a hop count rather than a proxy address list is deliberate --
	// platform proxy IPs are neither stable nor documented, and blindly
	// trusting the header is worse than not parsing it at all.
	TrustedProxyHops int `yaml:"trusted_proxy_hops,omitempty"`
}

// OwnerConfig is Eggy's system-wide identity: the one owner every surface
// (Telegram, web, schedules) authorizes against, independent of
// how any one surface authenticates that owner. See TelegramConfig.OwnerID
// for Telegram's own surface-specific numeric chat ID, which the Telegram
// adapter maps onto this ID rather than the reverse.
type OwnerConfig struct {
	ID string `yaml:"id"`
}

type TelegramConfig struct {
	OwnerID int64 `yaml:"owner_id"`
}

// Configured reports whether Telegram is a channel for this deployment.
// Telegram is optional: a web-only deployment omits the block entirely and
// sets owner.id directly, and then needs no bot token, no webhook secret,
// and no numeric Telegram owner ID.
func (c TelegramConfig) Configured() bool { return c.OwnerID != 0 }

type RepositoryConfig struct {
	Name              string   `yaml:"name"`
	CloneURL          string   `yaml:"clone_url"`
	BaseBranch        string   `yaml:"base_branch"`
	ProtectedBranches []string `yaml:"protected_branches"`
	// Self marks the repository that holds Eggy's own source. It grants no
	// capability of its own -- it only tells the agent which registered
	// repository is its own body, so a self-improvement turn knows where
	// AGENTS.md and the published architecture guide describing it live. At
	// most one repository may set it.
	Self bool `yaml:"self,omitempty"`
}

type RunnerConfig struct {
	Root           string   `yaml:"root"`
	Timeout        Duration `yaml:"timeout"`
	Retention      Duration `yaml:"retention"`
	MaxOutputBytes int64    `yaml:"max_output_bytes"`
	AllowedEnv     []string `yaml:"allowed_env"`
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers,omitempty"`
}

type MCPServerConfig struct {
	// URL belongs to the streamable-http transport; Command, Args, and
	// EnvAllowlist belong to stdio. Mixing the two sets is rejected rather
	// than silently ignored, so a config never looks like it configured a
	// transport it did not.
	URL       string   `yaml:"url,omitempty"`
	Transport string   `yaml:"transport"`
	Command   string   `yaml:"command,omitempty"`
	Args      []string `yaml:"args,omitempty"`
	// EnvAllowlist names the environment variables forwarded to a stdio
	// child. Nothing else crosses over, so credentials Eggy holds for other
	// capabilities stay outside the child unless a server asks for them by
	// name.
	EnvAllowlist   []string `yaml:"env_allowlist,omitempty"`
	Auth           string   `yaml:"auth"`
	BearerTokenEnv string   `yaml:"bearer_token_env,omitempty"`
	// OAuthClientID and OAuthClientSecretEnv are for an authorization server
	// that does not support dynamic client registration -- Google's, among
	// others -- where the owner registers the client by hand and supplies its
	// credentials. The client ID is not a secret (it travels in the
	// authorization URL's query string, visible in the browser), so it lives
	// in YAML; the secret never does, and is named here and read from the
	// environment like every other secret. Leaving the secret env empty is a
	// public client, authorized by PKCE alone.
	OAuthClientID             string   `yaml:"oauth_client_id,omitempty"`
	OAuthClientSecretEnv      string   `yaml:"oauth_client_secret_env,omitempty"`
	OAuthScopes               []string `yaml:"oauth_scopes,omitempty"`
	Enabled                   bool     `yaml:"enabled"`
	ConnectTimeout            Duration `yaml:"connect_timeout"`
	Timeout                   Duration `yaml:"timeout"`
	MaxOutputBytes            int64    `yaml:"max_output_bytes"`
	SupportsParallelToolCalls bool     `yaml:"supports_parallel_tool_calls"`
	// FailureThreshold and Cooldown are the per-tool failure policy: this
	// many consecutive failures of one tool take that tool, and only that
	// tool, out of service for this long.
	FailureThreshold int                 `yaml:"failure_threshold"`
	Cooldown         Duration            `yaml:"cooldown"`
	ToolFilter       MCPToolFilterConfig `yaml:"tool_filter"`
	// RequireApproval names the remote tools on this server whose calls the
	// owner must approve before they run. A configured MCP server is otherwise
	// trusted wholesale, so this is what lets a mutation arriving over MCP
	// carry the same per-call approval a native tool's would.
	RequireApproval []string `yaml:"require_approval,omitempty"`
}

type MCPToolFilterConfig struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

type Secrets struct {
	TelegramBotToken      string
	TelegramWebhookSecret string
	ProviderAPIKeys       map[string]string
	GitHubToken           string
	EncryptionKey         string
	MCPBearerTokens       map[string]string
	MCPOAuthClientSecrets map[string]string
	GoogleClientSecret    string
	TavilyAPIKey          string
	UIUserEmail           string
	UIPassword            string
}

// Values returns every secret Eggy currently holds, for redaction. Empty
// values are skipped: redacting "" would replace every byte of every line.
func (s Secrets) Values() []string {
	values := []string{
		s.TelegramBotToken, s.TelegramWebhookSecret, s.GitHubToken,
		s.EncryptionKey,
		s.GoogleClientSecret,
		s.TavilyAPIKey,
		s.UIPassword,
	}
	for _, key := range s.ProviderAPIKeys {
		values = append(values, key)
	}
	for _, secret := range s.MCPOAuthClientSecrets {
		values = append(values, secret)
	}
	for _, token := range s.MCPBearerTokens {
		values = append(values, token)
	}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// SecretsFromEnv reads every secret whose environment variable name is fixed.
// Provider API keys and MCP bearer tokens are not among them: their variable
// names are chosen in config.yaml, so LoadConfig fills those in once the file
// parses. This exists separately because a process that cannot load its config
// still needs the owner's web credential to serve safe mode, and still needs
// these values for log redaction.
func SecretsFromEnv(getenv func(string) string) Secrets {
	return Secrets{
		TelegramBotToken: getenv("TELEGRAM_BOT_TOKEN"), TelegramWebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"),
		GitHubToken:           getenv("GITHUB_TOKEN"),
		EncryptionKey:         getenv("EGGY_ENCRYPTION_KEY"),
		UIUserEmail:           getenv("EGGY_UI_USER_EMAIL"),
		UIPassword:            getenv("EGGY_UI_PASSWORD"),
		ProviderAPIKeys:       map[string]string{},
		MCPBearerTokens:       map[string]string{},
		MCPOAuthClientSecrets: map[string]string{},
	}
}

func LoadConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, Secrets{}, fmt.Errorf("open config: %w", err)
	}
	if err := decodeKnownYAML(data, &cfg); err != nil {
		return cfg, Secrets{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.applyDefaults(); err != nil {
		return cfg, Secrets{}, err
	}
	if err := applyRuntimeOverrides(&cfg, getenv); err != nil {
		return cfg, Secrets{}, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, Secrets{}, err
	}
	secrets := SecretsFromEnv(getenv)
	for name, provider := range cfg.Providers {
		secrets.ProviderAPIKeys[name] = getenv(provider.APIKeyEnv)
	}
	for name, server := range cfg.MCP.Servers {
		if server.Auth == "bearer-env" {
			secrets.MCPBearerTokens[name] = getenv(server.BearerTokenEnv)
		}
		if server.OAuthClientSecretEnv != "" {
			secrets.MCPOAuthClientSecrets[name] = getenv(server.OAuthClientSecretEnv)
		}
	}
	if cfg.Google.ClientSecretEnv != "" {
		secrets.GoogleClientSecret = getenv(cfg.Google.ClientSecretEnv)
	}
	if cfg.Tavily.Enabled && cfg.Tavily.APIKeyEnv != "" {
		secrets.TavilyAPIKey = getenv(cfg.Tavily.APIKeyEnv)
	}
	if err := cfg.validateSecrets(secrets); err != nil {
		return cfg, Secrets{}, err
	}
	return cfg, secrets, nil
}

func decodeKnownYAML(data []byte, destination any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(destination)
}

func applyRuntimeOverrides(cfg *Config, getenv func(string) string) error {
	raw := strings.TrimSpace(getenv("PORT"))
	if raw == "" {
		return nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be an integer between 1 and 65535")
	}
	cfg.Server.Listen = ":" + strconv.Itoa(port)
	return nil
}

func (c *Config) applyDefaults() error {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.TelegramWebhookPath == "" {
		c.Server.TelegramWebhookPath = "/webhooks/telegram"
	}
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	if c.Agent.Timezone == "" {
		c.Agent.Timezone = "UTC"
	}
	// A config written before owner.id existed only carries
	// telegram.owner_id; derive the canonical identity from it so existing
	// deployments keep working unchanged. A config that sets owner.id
	// directly (a web-only deployment with no Telegram section) needs no
	// Telegram configuration at all.
	if c.Owner.ID == "" && c.Telegram.OwnerID != 0 {
		c.Owner.ID = strconv.FormatInt(c.Telegram.OwnerID, 10)
	}
	if c.Runner.Root == "" {
		c.Runner.Root = filepath.Join(c.DataDir, "runs")
	}
	if c.Runner.Timeout == 0 {
		c.Runner.Timeout = Duration(45 * time.Minute)
	}
	if c.Runner.Retention == 0 {
		c.Runner.Retention = Duration(30 * time.Minute)
	}
	if c.Runner.MaxOutputBytes == 0 {
		c.Runner.MaxOutputBytes = 1 << 20
	}
	// Product names are matched by two independent readers -- the adapter
	// decides which tools exist, the wiring decides which scopes to request --
	// and validation accepts any casing. Canonicalizing here is what keeps
	// those readers from disagreeing: every load path runs applyDefaults, so
	// nothing downstream has to lowercase a product name again.
	c.Google.Products = normalizeProducts(c.Google.Products)
	if c.Tavily.APIKeyEnv == "" {
		c.Tavily.APIKeyEnv = "TAVILY_API_KEY"
	}
	if c.Tavily.SearchDepth == "" {
		c.Tavily.SearchDepth = "basic"
	}
	if c.Tavily.ExtractDepth == "" {
		c.Tavily.ExtractDepth = "basic"
	}
	if c.Tavily.MaxResults == 0 {
		c.Tavily.MaxResults = 5
	}
	if c.Tavily.MaxOutputBytes == 0 {
		c.Tavily.MaxOutputBytes = 64 << 10
	}
	if c.Tavily.Timeout == 0 {
		c.Tavily.Timeout = Duration(30 * time.Second)
	}
	if c.Tracing.KeepTurns == 0 {
		c.Tracing.KeepTurns = 500
	}
	if c.Tracing.Retention == 0 {
		c.Tracing.Retention = Duration(7 * 24 * time.Hour)
	}
	if c.Tracing.MaxBodyBytes == 0 {
		c.Tracing.MaxBodyBytes = 1 << 20
	}
	for name, server := range c.MCP.Servers {
		if server.ConnectTimeout == 0 {
			server.ConnectTimeout = Duration(10 * time.Second)
		}
		if server.Timeout == 0 {
			server.Timeout = Duration(time.Minute)
		}
		if server.MaxOutputBytes == 0 {
			server.MaxOutputBytes = 128 << 10
		}
		if server.FailureThreshold == 0 {
			server.FailureThreshold = 3
		}
		if server.Cooldown == 0 {
			server.Cooldown = Duration(30 * time.Second)
		}
		c.MCP.Servers[name] = server
	}
	return nil
}

var (
	branchPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	configuredNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	environmentNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	validReasoningEfforts  = map[string]bool{"low": true, "medium": true, "high": true, "max": true}
)

// TavilyConfig is Eggy's reach into the open web: web_search finds pages,
// web_extract reads them.
//
// It is a single on/off switch because it is a single API key. Disabled --
// which is the default -- no client is built, no tool is registered, and the
// two schemas cost nothing on any model call. That gate is the whole argument
// for shipping this as a core tool rather than the MCP server TODO.md R2
// originally called for: an owner who never wants Eggy reaching the internet
// pays nothing for the owners who do.
type TavilyConfig struct {
	Enabled bool `yaml:"enabled"`
	// APIKeyEnv names the variable holding the key. The key itself never
	// appears in YAML, like every other credential here.
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
	// SearchDepth and ExtractDepth trade credits for quality: advanced costs
	// two credits where basic, fast and ultra-fast cost one.
	SearchDepth  string `yaml:"search_depth,omitempty"`
	ExtractDepth string `yaml:"extract_depth,omitempty"`
	// MaxResults is the default used when the model does not ask for a count.
	MaxResults int `yaml:"max_results,omitempty"`
	// MaxOutputBytes bounds a whole response. Extracted page text is
	// unbounded at the source, and one long article can consume a turn's
	// context on its own.
	MaxOutputBytes int      `yaml:"max_output_bytes,omitempty"`
	Timeout        Duration `yaml:"timeout,omitempty"`
}

// knownTavilyDepths is duplicated from the adapter rather than imported, for
// the same reason knownGoogleProducts is: internal/config must not depend on a
// plugin package.
var (
	knownSearchDepths  = []string{"basic", "advanced", "fast", "ultra-fast"}
	knownExtractDepths = []string{"basic", "advanced"}
)

func (c Config) ActiveModel(alias string) (ProviderConfig, ModelAliasConfig, error) {
	model, ok := c.ModelAliases[alias]
	if !ok {
		return ProviderConfig{}, ModelAliasConfig{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	provider, ok := c.Providers[model.Provider]
	if !ok {
		return ProviderConfig{}, ModelAliasConfig{}, fmt.Errorf("model alias %q references unknown provider %q", alias, model.Provider)
	}
	return provider, model, nil
}
