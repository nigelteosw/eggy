package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
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

// Validate rejects a window that cannot work, rather than letting it silently
// suppress every beat.
func (h ActiveHours) Validate() error {
	start, end := strings.TrimSpace(h.Start), strings.TrimSpace(h.End)
	if start == "" && end == "" {
		return nil
	}
	if start == "" || end == "" {
		return errors.New("heartbeat.active_hours needs both start and end")
	}
	startMinutes, err := parseClock(start)
	if err != nil {
		return fmt.Errorf("heartbeat.active_hours.start: %w", err)
	}
	// 24:00 is accepted as an end so a window can run to midnight without
	// being written as the wrapped 00:00, which would mean the opposite.
	endMinutes, err := parseClock(end)
	if err != nil {
		return fmt.Errorf("heartbeat.active_hours.end: %w", err)
	}
	if startMinutes >= 24*60 {
		return errors.New("heartbeat.active_hours.start must be before 24:00")
	}
	if startMinutes == endMinutes {
		return errors.New("heartbeat.active_hours.start and end must differ")
	}
	return nil
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
}

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

// ValidateDocument reports whether data is a usable config.yaml, decoding it
// through the same strict path LoadConfig uses so an unknown key is caught
// rather than silently ignored. It stops at defaults plus Validate: an owner
// editing raw YAML may legitimately write settings this build does not read
// yet, and this is the check an editor runs before saving, not a loader.
func ValidateDocument(data []byte) error {
	var candidate Config
	if err := decodeKnownYAML(data, &candidate); err != nil {
		return fmt.Errorf("not valid YAML: %w", err)
	}
	if err := candidate.applyDefaults(); err != nil {
		return err
	}
	return candidate.Validate()
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Owner.ID) == "" {
		return errors.New("owner.id must be set")
	}
	// owner.id is the system-wide identity; Telegram is one optional channel
	// onto it. A negative owner_id is a typo rather than an omission, so it
	// is still rejected -- but omitting the block entirely is a web-only
	// deployment, not an error.
	if c.Telegram.OwnerID < 0 {
		return errors.New("telegram.owner_id must be positive when set")
	}
	if c.Telegram.Configured() && strconv.FormatInt(c.Telegram.OwnerID, 10) != c.Owner.ID {
		return errors.New("owner.id must match telegram.owner_id when Telegram is configured")
	}
	u, err := url.Parse(c.Server.PublicBaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("server.public_base_url must be an HTTP(S) URL")
	}
	if !strings.HasPrefix(c.Server.TelegramWebhookPath, "/") {
		return errors.New("server.telegram_webhook_path must begin with /")
	}
	if c.Server.TrustedProxyHops < 0 {
		return errors.New("server.trusted_proxy_hops must not be negative")
	}
	if err := c.validateProviders(); err != nil {
		return err
	}
	if c.Runner.Timeout.Value() <= 0 {
		return errors.New("runner.timeout must be positive")
	}
	if c.Runner.Retention.Value() <= 0 {
		return errors.New("runner.retention must be positive")
	}
	if c.Heartbeat.Interval < 0 {
		return errors.New("heartbeat.interval must not be negative")
	}
	if err := c.Heartbeat.ActiveHours.Validate(); err != nil {
		return err
	}
	if c.Runner.MaxOutputBytes <= 0 {
		return errors.New("runner.max_output_bytes must be positive")
	}
	if c.Runner.Root != "" && c.DataDir != "" && !pathWithin(c.DataDir, c.Runner.Root) {
		return errors.New("runner.root must be within data_dir for resumable implementation sessions")
	}
	if _, err := time.LoadLocation(c.Agent.Timezone); err != nil {
		return errors.New("agent.timezone must be a valid IANA timezone")
	}
	names := map[string]bool{}
	self := ""
	for _, repo := range c.Repositories {
		if repo.Self {
			if self != "" {
				return fmt.Errorf("repositories %q and %q both set self: exactly one repository is Eggy's own body", self, repo.Name)
			}
			self = repo.Name
		}
		if repo.Name == "" || names[repo.Name] {
			return fmt.Errorf("duplicate repository name %q", repo.Name)
		}
		names[repo.Name] = true
		if repo.CloneURL == "" {
			return fmt.Errorf("repository %q clone_url is required", repo.Name)
		}
		if !branchPattern.MatchString(repo.BaseBranch) {
			return fmt.Errorf("repository %q has invalid base branch", repo.Name)
		}
		for _, branch := range repo.ProtectedBranches {
			if !branchPattern.MatchString(branch) {
				return fmt.Errorf("repository %q has invalid protected branch %q", repo.Name, branch)
			}
		}
	}
	if err := c.validateMCP(); err != nil {
		return err
	}
	if err := c.validateGoogle(); err != nil {
		return err
	}
	if err := c.validateTavily(); err != nil {
		return err
	}
	// An unrecognized mode is refused rather than falling back to a working
	// one: an owner who wrote "readonly" meant to be asked about writes, and
	// quietly running them instead is the one failure this setting exists to
	// prevent.
	if mode := c.Approvals.Mode; mode != "" && !ports.ApprovalMode(mode).Valid() {
		return fmt.Errorf("approvals.mode %q must be strict, normal or auto", mode)
	}
	// Every tracing limit is a ceiling on durable state, so a stated one that
	// removes the ceiling is refused: the failure it would produce is a full
	// disk hours later, nowhere near the edit. Zero is "not stated" and picks
	// up the default in applyDefaults, exactly as every other optional
	// setting here does.
	if c.Tracing.KeepTurns < 0 {
		return errors.New("tracing.keep_turns must be at least 1")
	}
	if c.Tracing.Retention.Value() < 0 {
		return errors.New("tracing.retention must be positive")
	}
	if bytes := c.Tracing.MaxBodyBytes; bytes != 0 && bytes < 1024 {
		return errors.New("tracing.max_body_bytes must be at least 1024")
	}
	return nil
}

// validateTavily refuses a half-configured integration at load rather than at
// the first search, where it would present as a tool that mysteriously always
// fails. Defaults cover every field but the key, so in practice this catches
// the one thing the owner has to supply.
func (c Config) validateTavily() error {
	tavily := c.Tavily
	if !tavily.Enabled {
		return nil
	}
	if tavily.APIKeyEnv != "" && !environmentNamePattern.MatchString(tavily.APIKeyEnv) {
		return fmt.Errorf("tavily.api_key_env %q is not a valid environment variable name", tavily.APIKeyEnv)
	}
	// Validate is reachable without applyDefaults, so an empty depth is the
	// default rather than a rejection.
	if tavily.SearchDepth != "" && !slices.Contains(knownSearchDepths, tavily.SearchDepth) {
		return errors.New("tavily.search_depth must be one of: " + strings.Join(knownSearchDepths, ", "))
	}
	if tavily.ExtractDepth != "" && !slices.Contains(knownExtractDepths, tavily.ExtractDepth) {
		return errors.New("tavily.extract_depth must be one of: " + strings.Join(knownExtractDepths, ", "))
	}
	if tavily.MaxResults < 0 || tavily.MaxResults > 20 {
		return errors.New("tavily.max_results must be between 1 and 20")
	}
	// A budget too small to hold a useful answer is a misconfiguration, not a
	// preference: every result would come back truncated to nothing.
	if tavily.MaxOutputBytes != 0 && tavily.MaxOutputBytes < 4096 {
		return errors.New("tavily.max_output_bytes must be at least 4096")
	}
	if tavily.Timeout < 0 {
		return errors.New("tavily.timeout must not be negative")
	}
	return nil
}

// validateGoogle refuses a half-configured integration rather than letting it
// fail at the first tool call. An enabled Google with no client id authorizes
// nothing; an unknown product name is almost always a typo that would
// otherwise present as a missing tool with no explanation.
func (c Config) validateGoogle() error {
	google := c.Google
	if !google.Enabled {
		return nil
	}
	if strings.TrimSpace(google.ClientID) == "" {
		return errors.New("google.client_id is required when google.enabled is true")
	}
	if google.ClientSecretEnv != "" && !environmentNamePattern.MatchString(google.ClientSecretEnv) {
		return fmt.Errorf("google.client_secret_env %q is not a valid environment variable name", google.ClientSecretEnv)
	}
	if len(google.Products) == 0 {
		return errors.New("google.products must name at least one of: " + strings.Join(knownGoogleProducts, ", "))
	}
	// Normalized rather than compared as written: Validate is reachable without
	// applyDefaults (SetGoogle validates a candidate it built itself), so this
	// must not depend on canonicalization having already run.
	for _, product := range normalizeProducts(google.Products) {
		if !slices.Contains(knownGoogleProducts, product) {
			return fmt.Errorf("unknown google product %q; known products are %s", product, strings.Join(knownGoogleProducts, ", "))
		}
	}
	for _, scope := range google.Scopes {
		if !strings.HasPrefix(scope, "https://") {
			return fmt.Errorf("google scope %q must be a full https scope URL", scope)
		}
	}
	if google.MaxOutputBytes < 0 {
		return errors.New("google.max_output_bytes must not be negative")
	}
	return nil
}

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

// knownGoogleProducts is duplicated from the adapter's own list rather than
// imported: internal/config must not depend on a plugin package, and the
// adapter's Tools function is the one place that pins the pairing.
var knownGoogleProducts = []string{"calendar", "contacts", "docs", "drive", "gmail", "sheets"}

// normalizeProducts is the one spelling rule for a product name: lowercase,
// trimmed, and empties dropped. Order is the owner's and is preserved.
func normalizeProducts(products []string) []string {
	if len(products) == 0 {
		return products
	}
	normalized := make([]string, 0, len(products))
	for _, product := range products {
		if trimmed := strings.ToLower(strings.TrimSpace(product)); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func (c Config) validateMCP() error {
	for name, server := range c.MCP.Servers {
		if !configuredNamePattern.MatchString(name) {
			return fmt.Errorf("invalid MCP server name %q", name)
		}
		switch server.Transport {
		case "streamable-http":
			u, err := url.Parse(server.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("MCP server %q URL must use HTTPS", name)
			}
			if u.User != nil {
				return fmt.Errorf("MCP server %q URL must not contain credentials", name)
			}
			if server.Command != "" || len(server.Args) > 0 || len(server.EnvAllowlist) > 0 {
				return fmt.Errorf("MCP server %q command, args, and env_allowlist apply only to the stdio transport", name)
			}
		case "stdio":
			if err := validateStdioMCP(name, server); err != nil {
				return err
			}
		default:
			return fmt.Errorf("MCP server %q has unsupported transport %q", name, server.Transport)
		}
		if server.Auth != "oauth" && server.Auth != "bearer-env" && server.Auth != "none" {
			return fmt.Errorf("MCP server %q has unsupported auth %q", name, server.Auth)
		}
		if server.Auth == "bearer-env" && !environmentNamePattern.MatchString(server.BearerTokenEnv) {
			return fmt.Errorf("MCP server %q bearer_token_env is invalid", name)
		}
		if server.OAuthClientSecretEnv != "" && !environmentNamePattern.MatchString(server.OAuthClientSecretEnv) {
			return fmt.Errorf("MCP server %q oauth_client_secret_env is invalid", name)
		}
		if (server.OAuthClientID != "" || server.OAuthClientSecretEnv != "") && server.Auth != "oauth" {
			return fmt.Errorf("MCP server %q oauth client credentials apply only to auth oauth", name)
		}
		if server.OAuthClientSecretEnv != "" && server.OAuthClientID == "" {
			return fmt.Errorf("MCP server %q sets oauth_client_secret_env without oauth_client_id", name)
		}
		if server.ConnectTimeout.Value() <= 0 || server.Timeout.Value() <= 0 || server.MaxOutputBytes <= 0 {
			return fmt.Errorf("MCP server %q timeouts and max_output_bytes must be positive", name)
		}
		if server.FailureThreshold < 0 || server.Cooldown < 0 {
			return fmt.Errorf("MCP server %q failure_threshold and cooldown must not be negative", name)
		}
		for _, filter := range [][]string{server.ToolFilter.Include, server.ToolFilter.Exclude} {
			seen := map[string]bool{}
			for _, tool := range filter {
				if strings.TrimSpace(tool) == "" {
					return fmt.Errorf("MCP server %q tool filters must not contain empty names", name)
				}
				if seen[tool] {
					return fmt.Errorf("MCP server %q has duplicate tool filter %q", name, tool)
				}
				seen[tool] = true
			}
		}
		approvals := map[string]bool{}
		for _, tool := range server.RequireApproval {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("MCP server %q require_approval must not contain empty names", name)
			}
			if approvals[tool] {
				return fmt.Errorf("MCP server %q has duplicate require_approval entry %q", name, tool)
			}
			approvals[tool] = true
		}
		// An excluded tool never reaches the catalog, so requiring approval for
		// it is a contradiction: the owner wrote down a gate for a call that
		// cannot happen, which most likely means the gate they wanted is on a
		// tool that is still ungated.
		for _, tool := range server.ToolFilter.Exclude {
			if approvals[tool] {
				return fmt.Errorf("MCP server %q requires approval for excluded tool %q", name, tool)
			}
		}
	}
	return nil
}

// validateStdioMCP checks the fields only a stdio server may set. A stdio
// server is a local subprocess, so it has no URL and no HTTP authorization
// mode: its authorization is whatever the allowlisted environment grants it.
func validateStdioMCP(name string, server MCPServerConfig) error {
	if server.URL != "" {
		return fmt.Errorf("MCP server %q url applies only to the streamable-http transport", name)
	}
	if strings.TrimSpace(server.Command) == "" {
		return fmt.Errorf("MCP server %q must set a command for the stdio transport", name)
	}
	if server.Auth != "none" {
		return fmt.Errorf("MCP server %q must use auth none for the stdio transport", name)
	}
	for _, arg := range server.Args {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("MCP server %q args must not contain empty values", name)
		}
	}
	seen := map[string]bool{}
	for _, variable := range server.EnvAllowlist {
		if !environmentNamePattern.MatchString(variable) {
			return fmt.Errorf("MCP server %q env_allowlist entry %q is invalid", name, variable)
		}
		if seen[variable] {
			return fmt.Errorf("MCP server %q has duplicate env_allowlist entry %q", name, variable)
		}
		seen[variable] = true
	}
	return nil
}

func pathWithin(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absRoot, absPath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// supportedModelAdapters is the wire format a provider speaks, not the vendor
// it belongs to. "openai_compatible" is OpenAI's Chat Completions shape, which
// OpenAI itself, DeepSeek, OpenRouter, Groq, and most hosted models serve --
// reaching any of them is a providers entry, not a new adapter.
//
// A vendor whose wire format genuinely differs (Anthropic's Messages API,
// with its top-level system prompt, content blocks, and required max_tokens)
// is what earns a new name here plus a case in bootstrap's newModelAdapter.
// Those two lists are deliberately kept in step: this one decides what config
// accepts, that one decides what config gets.
var supportedModelAdapters = []string{"openai_compatible"}

func (c Config) validateProviders() error {
	if !configuredNamePattern.MatchString(c.Agent.DefaultModel) {
		return errors.New("agent.default_model must name a configured model alias")
	}
	for name, provider := range c.Providers {
		if !configuredNamePattern.MatchString(name) {
			return fmt.Errorf("invalid provider name %q", name)
		}
		if !slices.Contains(supportedModelAdapters, provider.Adapter) {
			return fmt.Errorf("unsupported provider adapter %q; supported adapters are %s", provider.Adapter, strings.Join(supportedModelAdapters, ", "))
		}
		u, err := url.Parse(provider.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("provider %q base_url must be an HTTP(S) URL", name)
		}
		if !environmentNamePattern.MatchString(provider.APIKeyEnv) {
			return fmt.Errorf("provider %q api_key_env is invalid", name)
		}
	}
	for alias, model := range c.ModelAliases {
		if !configuredNamePattern.MatchString(alias) {
			return fmt.Errorf("invalid model alias %q", alias)
		}
		if strings.TrimSpace(model.Model) == "" {
			return fmt.Errorf("model alias %q model is required", alias)
		}
		if _, ok := c.Providers[model.Provider]; !ok {
			return fmt.Errorf("model alias %q references unknown provider %q", alias, model.Provider)
		}
		for _, effort := range model.ReasoningEfforts {
			if !validReasoningEfforts[effort] {
				return fmt.Errorf("model alias %q has invalid reasoning effort %q", alias, effort)
			}
		}
	}
	if _, ok := c.ModelAliases[c.Agent.DefaultModel]; !ok {
		return fmt.Errorf("agent.default_model %q is not configured", c.Agent.DefaultModel)
	}
	return nil
}

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

// validateSecrets fails boot on a credential a configured capability needs but
// the environment does not hold. Each capability declares its own requirements
// through require, so a capability that is switched off costs nothing and a new
// one adds a line rather than a branch in a shared list.
func (c Config) validateSecrets(s Secrets) error {
	var missing string
	require := func(name, value string) {
		if missing == "" && strings.TrimSpace(value) == "" {
			missing = name
		}
	}
	// Telegram credentials are required only when Telegram is a channel for
	// this deployment; a web-only one must not have to invent them.
	if c.Telegram.Configured() {
		require("TELEGRAM_BOT_TOKEN", s.TelegramBotToken)
		require("TELEGRAM_WEBHOOK_SECRET", s.TelegramWebhookSecret)
	}
	usedProviders := map[string]bool{}
	for _, model := range c.ModelAliases {
		usedProviders[model.Provider] = true
	}
	for providerName := range usedProviders {
		require(c.Providers[providerName].APIKeyEnv, s.ProviderAPIKeys[providerName])
	}
	if len(c.Repositories) > 0 {
		require("GITHUB_TOKEN", s.GitHubToken)
	}
	for name, server := range c.MCP.Servers {
		if !server.Enabled {
			continue
		}
		if server.Auth == "oauth" {
			require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
		}
		if server.Auth == "bearer-env" {
			require(server.BearerTokenEnv, s.MCPBearerTokens[name])
		}
		if server.OAuthClientSecretEnv != "" {
			require(server.OAuthClientSecretEnv, s.MCPOAuthClientSecrets[name])
		}
	}
	if c.Google.Enabled {
		// The token lands in the same encrypted auth.json the MCP records use,
		// so the key is required for the same reason.
		require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
		if c.Google.ClientSecretEnv != "" {
			require(c.Google.ClientSecretEnv, s.GoogleClientSecret)
		}
	}
	if c.Tavily.Enabled && c.Tavily.APIKeyEnv != "" {
		require(c.Tavily.APIKeyEnv, s.TavilyAPIKey)
	}
	if strings.TrimSpace(s.UIUserEmail) != "" || strings.TrimSpace(s.UIPassword) != "" {
		require("EGGY_UI_USER_EMAIL", s.UIUserEmail)
		require("EGGY_UI_PASSWORD", s.UIPassword)
		require("EGGY_ENCRYPTION_KEY", s.EncryptionKey)
	}
	if missing != "" {
		return fmt.Errorf("required environment variable %s is missing", missing)
	}
	return nil
}
