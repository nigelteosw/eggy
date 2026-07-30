package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
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

type Config struct {
	Server       ServerConfig                `yaml:"server"`
	DataDir      string                      `yaml:"data_dir"`
	Owner        OwnerConfig                 `yaml:"owner"`
	Telegram     TelegramConfig              `yaml:"telegram,omitempty"`
	Agent        AgentConfig                 `yaml:"-"`
	Providers    map[string]ProviderConfig   `yaml:"-"`
	ModelAliases map[string]ModelAliasConfig `yaml:"-"`
	Repositories []RepositoryConfig          `yaml:"repositories"`
	Runner       RunnerConfig                `yaml:"runner"`
	Calendar     CalendarConfig              `yaml:"calendar,omitempty"`
	MCP          MCPConfig                   `yaml:"mcp,omitempty"`
}

// CalendarConfig configures the native Google Calendar integration. An empty
// section is the off switch: with no default_calendar there are no calendar
// tools, no OAuth routes, and no prompt bytes. Event times are read and
// written in agent.timezone.
type CalendarConfig struct {
	DefaultCalendar string `yaml:"default_calendar,omitempty"`
}

// Configured reports whether Calendar should be wired at all.
func (c CalendarConfig) Configured() bool { return strings.TrimSpace(c.DefaultCalendar) != "" }

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
	// AGENTS.md and docs/ARCHITECTURE.md describing it live. At most one
	// repository may set it.
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
	EnvAllowlist              []string `yaml:"env_allowlist,omitempty"`
	Auth                      string   `yaml:"auth"`
	BearerTokenEnv            string   `yaml:"bearer_token_env,omitempty"`
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
	GoogleClientID        string
	GoogleClientSecret    string
	EncryptionKey         string
	MCPBearerTokens       map[string]string
	UIUserEmail           string
	UIPassword            string
}

// Values returns every secret Eggy currently holds, for redaction. Empty
// values are skipped: redacting "" would replace every byte of every line.
func (s Secrets) Values() []string {
	values := []string{
		s.TelegramBotToken, s.TelegramWebhookSecret, s.GitHubToken,
		s.GoogleClientID, s.GoogleClientSecret, s.EncryptionKey,
		s.UIPassword,
	}
	for _, key := range s.ProviderAPIKeys {
		values = append(values, key)
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

type commonConfigDocument struct {
	Server       ServerConfig       `yaml:"server"`
	DataDir      string             `yaml:"data_dir"`
	Owner        OwnerConfig        `yaml:"owner"`
	Telegram     TelegramConfig     `yaml:"telegram,omitempty"`
	Repositories []RepositoryConfig `yaml:"repositories"`
	Runner       RunnerConfig       `yaml:"runner"`
	Calendar     CalendarConfig     `yaml:"calendar,omitempty"`
	MCP          MCPConfig          `yaml:"mcp,omitempty"`
}

type configDocument struct {
	Agent                AgentConfig                 `yaml:"agent"`
	Providers            map[string]ProviderConfig   `yaml:"providers"`
	Models               map[string]ModelAliasConfig `yaml:"models"`
	commonConfigDocument `yaml:",inline"`
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
		GitHubToken:        getenv("GITHUB_TOKEN"),
		GoogleClientID:     getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),
		EncryptionKey:      getenv("EGGY_ENCRYPTION_KEY"),
		UIUserEmail:        getenv("EGGY_UI_USER_EMAIL"),
		UIPassword:         getenv("EGGY_UI_PASSWORD"),
		ProviderAPIKeys:    map[string]string{},
		MCPBearerTokens:    map[string]string{},
	}
}

func LoadConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, Secrets{}, fmt.Errorf("open config: %w", err)
	}
	var document configDocument
	if err := decodeKnownYAML(data, &document); err != nil {
		return cfg, Secrets{}, fmt.Errorf("decode config: %w", err)
	}
	cfg = normalizeConfig(document)
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

func normalizeConfig(document configDocument) Config {
	common := document.commonConfigDocument
	return Config{
		Server: common.Server, DataDir: common.DataDir, Owner: common.Owner, Telegram: common.Telegram,
		Agent: document.Agent, Providers: document.Providers, ModelAliases: document.Models,
		Repositories: common.Repositories, Runner: common.Runner, Calendar: common.Calendar, MCP: common.MCP,
	}
}

func (c Config) commonDocument() commonConfigDocument {
	return commonConfigDocument{Server: c.Server, DataDir: c.DataDir, Owner: c.Owner, Telegram: c.Telegram, Repositories: c.Repositories, Runner: c.Runner, Calendar: c.Calendar, MCP: c.MCP}
}

func (c Config) MarshalYAML() (any, error) {
	return configDocument{Agent: c.Agent, Providers: c.Providers, Models: c.ModelAliases, commonConfigDocument: c.commonDocument()}, nil
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
	var document configDocument
	if err := decodeKnownYAML(data, &document); err != nil {
		return fmt.Errorf("not valid YAML: %w", err)
	}
	candidate := normalizeConfig(document)
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
	return nil
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

func (c Config) validateProviders() error {
	if !configuredNamePattern.MatchString(c.Agent.DefaultModel) {
		return errors.New("agent.default_model must name a configured model alias")
	}
	for name, provider := range c.Providers {
		if !configuredNamePattern.MatchString(name) {
			return fmt.Errorf("invalid provider name %q", name)
		}
		if provider.Adapter != "openai_compatible" {
			return fmt.Errorf("unsupported provider adapter %q", provider.Adapter)
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

func (c Config) validateSecrets(s Secrets) error {
	var required []struct{ name, value string }
	// Telegram credentials are required only when Telegram is a channel for
	// this deployment; a web-only one must not have to invent them.
	if c.Telegram.Configured() {
		required = append(required,
			struct{ name, value string }{"TELEGRAM_BOT_TOKEN", s.TelegramBotToken},
			struct{ name, value string }{"TELEGRAM_WEBHOOK_SECRET", s.TelegramWebhookSecret})
	}
	usedProviders := map[string]bool{}
	for _, model := range c.ModelAliases {
		usedProviders[model.Provider] = true
	}
	for providerName := range usedProviders {
		provider := c.Providers[providerName]
		required = append(required, struct{ name, value string }{provider.APIKeyEnv, s.ProviderAPIKeys[providerName]})
	}
	if len(c.Repositories) > 0 {
		required = append(required, struct{ name, value string }{"GITHUB_TOKEN", s.GitHubToken})
	}
	if c.Calendar.Configured() {
		required = append(required,
			struct{ name, value string }{"GOOGLE_CLIENT_ID", s.GoogleClientID},
			struct{ name, value string }{"GOOGLE_CLIENT_SECRET", s.GoogleClientSecret},
			struct{ name, value string }{"EGGY_ENCRYPTION_KEY", s.EncryptionKey})
	}
	for name, server := range c.MCP.Servers {
		if !server.Enabled {
			continue
		}
		if server.Auth == "oauth" {
			required = append(required, struct{ name, value string }{"EGGY_ENCRYPTION_KEY", s.EncryptionKey})
		}
		if server.Auth == "bearer-env" {
			required = append(required, struct{ name, value string }{server.BearerTokenEnv, s.MCPBearerTokens[name]})
		}
	}
	if strings.TrimSpace(s.UIUserEmail) != "" || strings.TrimSpace(s.UIPassword) != "" {
		required = append(required,
			struct{ name, value string }{"EGGY_UI_USER_EMAIL", s.UIUserEmail},
			struct{ name, value string }{"EGGY_UI_PASSWORD", s.UIPassword},
			struct{ name, value string }{"EGGY_ENCRYPTION_KEY", s.EncryptionKey})
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("required environment variable %s is missing", item.name)
		}
	}
	return nil
}
