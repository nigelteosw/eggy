package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

func LoadOrCreateConfig(path string, getenv func(string) string) (Config, Secrets, error) {
	if _, err := os.Stat(path); err == nil {
		if err := pruneRetiredFields(path); err != nil {
			return Config{}, Secrets{}, err
		}
		if err := migrateLegacyRunnerRoot(path); err != nil {
			return Config{}, Secrets{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, Secrets{}, fmt.Errorf("stat config: %w", err)
	} else if err := initializeConfig(path, getenv); err != nil {
		return Config{}, Secrets{}, err
	}
	// After both paths, and after the prune: a config that is upgraded and one
	// that is generated should end up describing the same settings, and a
	// section cannot be both retired and backfilled without the order saying
	// which wins. Adding a section never changes what the config means -- it
	// writes the defaults the absence already implied -- so this is safe to do
	// on the way into every boot.
	if err := backfillDefaultedSections(path); err != nil {
		return Config{}, Secrets{}, err
	}
	return LoadConfig(path, getenv)
}

// retiredConfigFields names settings earlier Eggy versions wrote that the
// current document has no field for, each as its path from the document root.
// Decoding is strict so that a misspelled key is an error instead of a setting
// that silently does nothing -- which also means a home directory written by an
// older build cannot start at all until its retired keys are gone. Removing
// them here keeps an upgrade from turning into a hand-edit on a mounted volume.
//
// A key belongs on this list only once the behaviour behind it is gone. One
// that moved rather than disappeared is a rename to carry over, not a prune.
var retiredConfigFields = [][]string{
	{"embeddings"},              // semantic recall
	{"implementation_sessions"}, // agent shell sessions
	{"scheduler"},               // heartbeat and proactive messaging
	{"calendar"},                // native Calendar; use an MCP calendar server
}

// carryOverCalendarTimezone moves a retired calendar.timezone to agent.timezone
// before the prune removes the whole section. One clock resolves every relative
// range, and the calendar's was the only one an older config stated out loud:
// dropping it would silently move an owner in Asia/Singapore onto the UTC
// default. An agent.timezone already present wins -- it is the current setting.
func carryOverCalendarTimezone(root *yaml.Node) bool {
	timezone := mappingValue(mappingValue(root, "calendar"), "timezone")
	if timezone == nil || strings.TrimSpace(timezone.Value) == "" {
		return false
	}
	agent := mappingValue(root, "agent")
	if agent == nil {
		agent = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "agent"}, agent)
	}
	if existing := mappingValue(agent, "timezone"); existing != nil {
		if strings.TrimSpace(existing.Value) != "" {
			return false
		}
		existing.Value = timezone.Value
		return true
	}
	agent.Content = append(agent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "timezone"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: timezone.Value})
	return true
}

// mappingValue returns the value node stored at key, or nil when the node is
// not a mapping or has no such key.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// pruneRetiredFields rewrites path without any retired key, leaving every
// remaining key, value, and comment exactly as the owner wrote it. A config
// that has none is not rewritten at all.
func pruneRetiredFields(path string) error {
	return filelock.With(path, func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("open config: %w", err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode config: %w", err)
		}
		if len(document.Content) == 0 {
			return nil
		}
		root := document.Content[0]
		changed := carryOverCalendarTimezone(root)
		removed := make([]string, 0, len(retiredConfigFields))
		for _, field := range retiredConfigFields {
			if deleteMappingKey(root, field) {
				removed = append(removed, strings.Join(field, "."))
			}
		}
		if len(removed) == 0 && !changed {
			return nil
		}
		if err := writeYAMLDocument(path, &document); err != nil {
			return err
		}
		// Logging is not configured until after the config loads, so this
		// goes to the default logger on purpose: the owner should see which
		// settings stopped applying, not discover it from behaviour.
		if len(removed) > 0 {
			slog.Warn("dropped retired config settings", "path", path, "settings", strings.Join(removed, ", "))
		}
		return nil
	})
}

// deleteMappingKey removes the entry at field from a YAML mapping node,
// reporting whether it was there. Mapping content alternates key, value, so an
// entry is a pair and both halves go.
func deleteMappingKey(node *yaml.Node, field []string) bool {
	if node == nil || node.Kind != yaml.MappingNode || len(field) == 0 {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != field[0] {
			continue
		}
		if len(field) > 1 {
			return deleteMappingKey(node.Content[i+1], field[1:])
		}
		node.Content = append(node.Content[:i], node.Content[i+2:]...)
		return true
	}
	return false
}

// migrateLegacyRunnerRoot upgrades Eggy's former temporary default without
// accepting arbitrary workspace paths outside the persistent data directory.
func migrateLegacyRunnerRoot(path string) error {
	return mutate(path, func(cfg *Config) error {
		if filepath.Clean(cfg.Runner.Root) != "/tmp/runs" {
			return errNoConfigChange
		}
		cfg.Runner.Root = filepath.Join(cfg.DataDir, "runs")
		return nil
	})
}

func initializeConfig(path string, getenv func(string) string) error {
	return filelock.With(path, func() error {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat config: %w", err)
		}
		cfg, err := firstBootConfig(getenv)
		if err != nil {
			return fmt.Errorf("generate config: %w", err)
		}
		body, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal generated config: %w", err)
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
		if err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if err := temporary.Chmod(0o600); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if _, err := temporary.Write(body); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := temporary.Sync(); err != nil {
			temporary.Close()
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := temporary.Close(); err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("persist generated config: %w", err)
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("persist generated config: %w", err)
		}
		return nil
	})
}

func firstBootConfig(getenv func(string) string) (Config, error) {
	// EGGY_TELEGRAM_OWNER_ID configures Telegram and derives the canonical
	// owner identity from it. A web-only deployment sets EGGY_OWNER_ID
	// instead and gets no Telegram block at all -- and so needs no bot
	// token or webhook secret either.
	var telegram TelegramConfig
	ownerValue := strings.TrimSpace(getenv("EGGY_TELEGRAM_OWNER_ID"))
	if ownerValue != "" {
		ownerID, err := strconv.ParseInt(ownerValue, 10, 64)
		if err != nil || ownerID <= 0 {
			return Config{}, errors.New("EGGY_TELEGRAM_OWNER_ID must be a positive integer")
		}
		telegram = TelegramConfig{OwnerID: ownerID}
	} else {
		ownerValue = strings.TrimSpace(getenv("EGGY_OWNER_ID"))
		if ownerValue == "" {
			return Config{}, errors.New("EGGY_TELEGRAM_OWNER_ID is required, or EGGY_OWNER_ID for a web-only deployment")
		}
	}
	publicBaseURL := strings.TrimSpace(getenv("EGGY_PUBLIC_BASE_URL"))
	if publicBaseURL == "" {
		domain := strings.TrimSpace(getenv("RAILWAY_PUBLIC_DOMAIN"))
		if domain == "" {
			return Config{}, errors.New("EGGY_PUBLIC_BASE_URL is required when RAILWAY_PUBLIC_DOMAIN is unavailable")
		}
		publicBaseURL = "https://" + domain
	}
	cfg := Config{
		Server: ServerConfig{
			Listen:              ":8080",
			PublicBaseURL:       publicBaseURL,
			TelegramWebhookPath: "/webhooks/telegram",
		},
		DataDir:  "/data",
		Owner:    OwnerConfig{ID: ownerValue},
		Telegram: telegram,
		Agent:    AgentConfig{DefaultModel: "deepseek-pro", Timezone: "Asia/Singapore"},
		Providers: map[string]ProviderConfig{
			"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"},
		},
		ModelAliases: map[string]ModelAliasConfig{
			"deepseek-pro": {Provider: "deepseek", Model: "deepseek-v4-pro"},
		},
		Repositories: []RepositoryConfig{},
		Runner: RunnerConfig{
			Root:           "/data/runs",
			Timeout:        Duration(45 * time.Minute),
			Retention:      Duration(30 * time.Minute),
			MaxOutputBytes: 1 << 20,
			AllowedEnv:     []string{"PATH", "LANG", "LC_ALL", "TERM"},
		},
	}
	if repositoryURL := strings.TrimSpace(getenv("EGGY_REPOSITORY_URL")); repositoryURL != "" {
		name := strings.TrimSpace(getenv("EGGY_REPOSITORY_NAME"))
		if name == "" {
			name = "eggy"
		}
		baseBranch := strings.TrimSpace(getenv("EGGY_REPOSITORY_BASE_BRANCH"))
		if baseBranch == "" {
			baseBranch = "main"
		}
		protectedBranches, err := firstBootProtectedBranches(getenv("EGGY_REPOSITORY_PROTECTED_BRANCHES"), baseBranch)
		if err != nil {
			return Config{}, err
		}
		cfg.Repositories = []RepositoryConfig{{Name: name, CloneURL: repositoryURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func firstBootProtectedBranches(raw, baseBranch string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{baseBranch}, nil
	}
	branches := make([]string, 0)
	for _, branch := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(branch); trimmed != "" {
			branches = append(branches, trimmed)
		}
	}
	if len(branches) == 0 {
		return nil, errors.New("EGGY_REPOSITORY_PROTECTED_BRANCHES must contain at least one branch")
	}
	return branches, nil
}
