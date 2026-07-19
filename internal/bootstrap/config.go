package bootstrap

import (
	"errors"
	"fmt"
	"net/url"
	"os"
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
	Version      int                `yaml:"version"`
	Server       ServerConfig       `yaml:"server"`
	DataDir      string             `yaml:"data_dir"`
	Telegram     TelegramConfig     `yaml:"telegram"`
	Models       ModelsConfig       `yaml:"models"`
	Repositories []RepositoryConfig `yaml:"repositories"`
	Runner       RunnerConfig       `yaml:"runner"`
	Scheduler    SchedulerConfig    `yaml:"scheduler"`
	Calendar     CalendarConfig     `yaml:"calendar"`
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
	DeepSeekAPIKey        string
	GitHubToken           string
	GoogleClientID        string
	GoogleClientSecret    string
	EncryptionKey         string
}

func LoadConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	var cfg Config
	f, err := os.Open(path)
	if err != nil {
		return cfg, Secrets{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
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
	secrets := Secrets{
		TelegramBotToken: getenv("TELEGRAM_BOT_TOKEN"), TelegramWebhookSecret: getenv("TELEGRAM_WEBHOOK_SECRET"),
		DeepSeekAPIKey: getenv("DEEPSEEK_API_KEY"), GitHubToken: getenv("GITHUB_TOKEN"),
		GoogleClientID: getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),
		EncryptionKey: getenv("EGGY_ENCRYPTION_KEY"),
	}
	if err := cfg.validateSecrets(secrets); err != nil {
		return cfg, Secrets{}, err
	}
	return cfg, secrets, nil
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
		c.Runner.Root = "/tmp/runs"
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
	if c.Models.Escalation.ToolSteps == 0 {
		c.Models.Escalation.ToolSteps = 4
	}
	if c.Models.Escalation.RecoverableFailures == 0 {
		c.Models.Escalation.RecoverableFailures = 2
	}
	return nil
}

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("version must be 1")
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
	if c.Models.Flash.ID == "" {
		return errors.New("models.flash.id is required")
	}
	if c.Models.Pro.ID == "" {
		return errors.New("models.pro.id is required")
	}
	if c.Models.Flash.Adapter != "deepseek" || c.Models.Pro.Adapter != "deepseek" {
		return errors.New("unsupported model adapter: models.flash.adapter and models.pro.adapter must be deepseek")
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

func (c Config) validateSecrets(s Secrets) error {
	required := []struct{ name, value string }{
		{"TELEGRAM_BOT_TOKEN", s.TelegramBotToken}, {"TELEGRAM_WEBHOOK_SECRET", s.TelegramWebhookSecret}, {"DEEPSEEK_API_KEY", s.DeepSeekAPIKey},
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
