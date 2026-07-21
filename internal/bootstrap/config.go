package bootstrap

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
	Version                int                         `yaml:"version"`
	Server                 ServerConfig                `yaml:"server"`
	DataDir                string                      `yaml:"data_dir"`
	Telegram               TelegramConfig              `yaml:"telegram"`
	Agent                  AgentConfig                 `yaml:"-"`
	Providers              map[string]ProviderConfig   `yaml:"-"`
	ModelAliases           map[string]ModelAliasConfig `yaml:"-"`
	Repositories           []RepositoryConfig          `yaml:"repositories"`
	Runner                 RunnerConfig                `yaml:"runner"`
	ImplementationSessions ImplementationSessionConfig `yaml:"implementation_sessions"`
	Scheduler              SchedulerConfig             `yaml:"scheduler"`
	Calendar               CalendarConfig              `yaml:"calendar"`
	legacyModels           ModelsConfig
}

type AgentConfig struct {
	DefaultModel string `yaml:"default_model"`
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
}

type TelegramConfig struct {
	OwnerID int64 `yaml:"owner_id"`
}

type ModelConfig struct {
	Adapter string `yaml:"adapter"`
	ID      string `yaml:"id"`
}

type EscalationConfig struct {
	ToolSteps           int `yaml:"tool_steps"`
	RecoverableFailures int `yaml:"recoverable_failures"`
}

type ModelsConfig struct {
	Flash      ModelConfig      `yaml:"flash"`
	Pro        ModelConfig      `yaml:"pro"`
	Escalation EscalationConfig `yaml:"escalation"`
}

type RepositoryConfig struct {
	Name              string   `yaml:"name"`
	CloneURL          string   `yaml:"clone_url"`
	BaseBranch        string   `yaml:"base_branch"`
	ProtectedBranches []string `yaml:"protected_branches"`
}

type RunnerConfig struct {
	Root           string   `yaml:"root"`
	Timeout        Duration `yaml:"timeout"`
	Retention      Duration `yaml:"retention"`
	MaxOutputBytes int64    `yaml:"max_output_bytes"`
	AllowedEnv     []string `yaml:"allowed_env"`
}

type ImplementationSessionConfig struct {
	ContextBudgetChars int `yaml:"context_budget_chars"`
	RecentMessages     int `yaml:"recent_messages"`
	OutputExcerptChars int `yaml:"output_excerpt_chars"`
}

type QuietHoursConfig struct {
	Start    string `yaml:"start"`
	End      string `yaml:"end"`
	Timezone string `yaml:"timezone"`
}

type SchedulerConfig struct {
	HeartbeatCadence         Duration         `yaml:"heartbeat_cadence"`
	QuietHours               QuietHoursConfig `yaml:"quiet_hours"`
	MinimumProactiveInterval Duration         `yaml:"minimum_proactive_interval"`
	WeeklyProactiveLimit     int              `yaml:"weekly_proactive_limit"`
}

type CalendarConfig struct {
	Enabled         bool   `yaml:"enabled"`
	DefaultCalendar string `yaml:"default_calendar"`
	Timezone        string `yaml:"timezone"`
}

type Secrets struct {
	TelegramBotToken      string
	TelegramWebhookSecret string
	ProviderAPIKeys       map[string]string
	GitHubToken           string
	GoogleClientID        string
	GoogleClientSecret    string
	EncryptionKey         string
}

type commonConfigDocument struct {
	Server                 ServerConfig                `yaml:"server"`
	DataDir                string                      `yaml:"data_dir"`
	Telegram               TelegramConfig              `yaml:"telegram"`
	Repositories           []RepositoryConfig          `yaml:"repositories"`
	Runner                 RunnerConfig                `yaml:"runner"`
	ImplementationSessions ImplementationSessionConfig `yaml:"implementation_sessions"`
	Scheduler              SchedulerConfig             `yaml:"scheduler"`
	Calendar               CalendarConfig              `yaml:"calendar"`
}

type legacyConfigDocument struct {
	Version              int          `yaml:"version"`
	Models               ModelsConfig `yaml:"models"`
	commonConfigDocument `yaml:",inline"`
}

type configV2Document struct {
	Version              int                         `yaml:"version"`
	Agent                AgentConfig                 `yaml:"agent"`
	Providers            map[string]ProviderConfig   `yaml:"providers"`
	Models               map[string]ModelAliasConfig `yaml:"models"`
	commonConfigDocument `yaml:",inline"`
}

func LoadConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, Secrets{}, fmt.Errorf("open config: %w", err)
	}
	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return cfg, Secrets{}, fmt.Errorf("decode config: %w", err)
	}
	switch header.Version {
	case 1:
		var document legacyConfigDocument
		if err := decodeKnownYAML(data, &document); err != nil {
			return cfg, Secrets{}, fmt.Errorf("decode config: %w", err)
		}
		cfg = normalizeLegacyConfig(document)
	case 2:
		var document configV2Document
		if err := decodeKnownYAML(data, &document); err != nil {
			return cfg, Secrets{}, fmt.Errorf("decode config: %w", err)
		}
		cfg = normalizeV2Config(document)
	default:
		return cfg, Secrets{}, fmt.Errorf("version must be 1 or 2")
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
	secrets := Secrets{
		TelegramBotToken: getenv("TELEGRAM_BOT_TOKEN"), TelegramWebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"),
		GitHubToken:    getenv("GITHUB_TOKEN"),
		GoogleClientID: getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),
		EncryptionKey:   getenv("EGGY_ENCRYPTION_KEY"),
		ProviderAPIKeys: map[string]string{},
	}
	for name, provider := range cfg.Providers {
		secrets.ProviderAPIKeys[name] = getenv(provider.APIKeyEnv)
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

func normalizeLegacyConfig(document legacyConfigDocument) Config {
	common := document.commonConfigDocument
	return Config{
		Version: document.Version, Server: common.Server, DataDir: common.DataDir, Telegram: common.Telegram,
		legacyModels: document.Models, Agent: AgentConfig{DefaultModel: "deepseek-pro"},
		Providers:    map[string]ProviderConfig{"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"}},
		ModelAliases: map[string]ModelAliasConfig{"deepseek-pro": {Provider: "deepseek", Model: document.Models.Pro.ID}},
		Repositories: common.Repositories, Runner: common.Runner, ImplementationSessions: common.ImplementationSessions, Scheduler: common.Scheduler, Calendar: common.Calendar,
	}
}

func normalizeV2Config(document configV2Document) Config {
	common := document.commonConfigDocument
	return Config{
		Version: document.Version, Server: common.Server, DataDir: common.DataDir, Telegram: common.Telegram,
		Agent: document.Agent, Providers: document.Providers, ModelAliases: document.Models,
		Repositories: common.Repositories, Runner: common.Runner, ImplementationSessions: common.ImplementationSessions, Scheduler: common.Scheduler, Calendar: common.Calendar,
	}
}

func (c Config) commonDocument() commonConfigDocument {
	return commonConfigDocument{Server: c.Server, DataDir: c.DataDir, Telegram: c.Telegram, Repositories: c.Repositories, Runner: c.Runner, ImplementationSessions: c.ImplementationSessions, Scheduler: c.Scheduler, Calendar: c.Calendar}
}

func (c Config) MarshalYAML() (any, error) {
	if c.Version == 2 {
		return configV2Document{Version: 2, Agent: c.Agent, Providers: c.Providers, Models: c.ModelAliases, commonConfigDocument: c.commonDocument()}, nil
	}
	return legacyConfigDocument{Version: c.Version, Models: c.legacyModels, commonConfigDocument: c.commonDocument()}, nil
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
	if c.ImplementationSessions.ContextBudgetChars == 0 {
		c.ImplementationSessions.ContextBudgetChars = 96000
	}
	if c.ImplementationSessions.RecentMessages == 0 {
		c.ImplementationSessions.RecentMessages = 16
	}
	if c.ImplementationSessions.OutputExcerptChars == 0 {
		c.ImplementationSessions.OutputExcerptChars = 8192
	}
	if c.legacyModels.Escalation.ToolSteps == 0 {
		c.legacyModels.Escalation.ToolSteps = 4
	}
	if c.legacyModels.Escalation.RecoverableFailures == 0 {
		c.legacyModels.Escalation.RecoverableFailures = 2
	}
	return nil
}

var (
	branchPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	configuredNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	environmentNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	validReasoningEfforts  = map[string]bool{"low": true, "medium": true, "high": true, "max": true}
)

func (c Config) Validate() error {
	if c.Version != 1 && c.Version != 2 {
		return fmt.Errorf("version must be 1 or 2")
	}
	if c.Telegram.OwnerID <= 0 {
		return errors.New("telegram.owner_id must be positive")
	}
	u, err := url.Parse(c.Server.PublicBaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("server.public_base_url must be an HTTP(S) URL")
	}
	if !strings.HasPrefix(c.Server.TelegramWebhookPath, "/") {
		return errors.New("server.telegram_webhook_path must begin with /")
	}
	if c.Version == 1 {
		if c.legacyModels.Flash.ID == "" {
			return errors.New("models.flash.id is required")
		}
		if c.legacyModels.Pro.ID == "" {
			return errors.New("models.pro.id is required")
		}
		if c.legacyModels.Flash.Adapter != "deepseek" || c.legacyModels.Pro.Adapter != "deepseek" {
			return errors.New("unsupported model adapter: models.flash.adapter and models.pro.adapter must be deepseek")
		}
	} else if err := c.validateProviders(); err != nil {
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
	if c.ImplementationSessions.ContextBudgetChars <= 0 || c.ImplementationSessions.RecentMessages <= 0 || c.ImplementationSessions.OutputExcerptChars <= 0 {
		return errors.New("implementation_sessions context_budget_chars, recent_messages, and output_excerpt_chars must be positive")
	}
	names := map[string]bool{}
	for _, repo := range c.Repositories {
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
	if c.Calendar.Enabled && c.Calendar.DefaultCalendar == "" {
		return errors.New("calendar.default_calendar is required")
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
	required := []struct{ name, value string }{
		{"TELEGRAM_BOT_TOKEN", s.TelegramBotToken}, {"TELEGRAM_WEBHOOK_SECRET", s.TelegramWebhookSecret},
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
	if c.Calendar.Enabled {
		required = append(required,
			struct{ name, value string }{"GOOGLE_CLIENT_ID", s.GoogleClientID}, struct{ name, value string }{"GOOGLE_CLIENT_SECRET", s.GoogleClientSecret},
			struct{ name, value string }{"EGGY_ENCRYPTION_KEY", s.EncryptionKey})
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("required environment variable %s is missing", item.name)
		}
	}
	return nil
}
