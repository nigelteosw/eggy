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
	} else {
		if !hasHeadlessIdentity(getenv) {
			if artifact, ok := existingHomeArtifact(path); ok {
				return Config{}, Secrets{}, fmt.Errorf("config is missing from an existing home containing %s; restore or repair config.yaml", artifact)
			}
			return Config{}, Secrets{}, ErrSetupRequired
		}
		if err := initializeConfig(path, getenv); err != nil {
			return Config{}, Secrets{}, err
		}
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
	var removed []string
	err := editYAMLDocument(path, func(root *yaml.Node) (bool, error) {
		changed := carryOverCalendarTimezone(root)
		for _, field := range retiredConfigFields {
			if deleteMappingKey(root, field) {
				removed = append(removed, strings.Join(field, "."))
			}
		}
		return changed || len(removed) > 0, nil
	})
	if err != nil {
		return err
	}
	// Logging is not configured until after the config loads, so this goes to
	// the default logger on purpose: the owner should see which settings
	// stopped applying, not discover it from behaviour.
	if len(removed) > 0 {
		slog.Warn("dropped retired config settings", "path", path, "settings", strings.Join(removed, ", "))
	}
	return nil
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
		cfg, err := firstBootConfig(filepath.Dir(path), getenv)
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

// firstBootAccounts parses EGGY_ACCOUNTS: comma-separated entries of
// id[:telegram_user_id]. Set, it selects the accounts shape for the
// generated config. The former id:google_email form is refused with the
// migration in mind: an address is not an identity here any more.
func firstBootAccounts(raw string) ([]AccountConfig, error) {
	var accounts []AccountConfig
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) > 2 || strings.Contains(entry, "@") {
			return nil, fmt.Errorf("EGGY_ACCOUNTS entry %q must be id or id:telegram_user_id; Google addresses are no longer part of an account (see the local login migration)", entry)
		}
		account := AccountConfig{ID: strings.TrimSpace(parts[0])}
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			userID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			if err != nil || userID <= 0 {
				return nil, fmt.Errorf("EGGY_ACCOUNTS entry %q has a non-numeric telegram_user_id", entry)
			}
			account.TelegramUserID = userID
		}
		accounts = append(accounts, account)
	}
	if len(accounts) == 0 {
		return nil, errors.New("EGGY_ACCOUNTS must list at least one id[:telegram_user_id] entry")
	}
	return accounts, nil
}

// firstBootConfig generates a config for the home at homeDir: the directory
// config.yaml is being written into, which is /data in a container only
// because the image sets EGGY_HOME there.
func firstBootConfig(homeDir string, getenv func(string) string) (Config, error) {
	// EGGY_ACCOUNTS selects the accounts shape: several people, one of whom
	// signs in with the environment credentials and the rest with local
	// passwords set later from the People card. Otherwise
	// EGGY_TELEGRAM_OWNER_ID configures Telegram and derives the canonical
	// owner identity from it, and a web-only deployment sets EGGY_OWNER_ID
	// instead and gets no Telegram block at all -- and so needs no bot token
	// or webhook secret either.
	var telegram TelegramConfig
	var accounts []AccountConfig
	var web WebConfig
	var expected string
	ownerValue := strings.TrimSpace(getenv("EGGY_TELEGRAM_OWNER_ID"))
	if raw := strings.TrimSpace(getenv("EGGY_ACCOUNTS")); raw != "" {
		parsed, err := firstBootAccounts(raw)
		if err != nil {
			return Config{}, err
		}
		accounts = parsed
		// One account binds itself; several need the operator to say which,
		// and EGGY_OWNER_ID is the identity already declared for that. List
		// order never decides it.
		web.PasswordAccountID = accounts[0].ID
		if len(accounts) > 1 {
			web.PasswordAccountID = strings.TrimSpace(getenv("EGGY_OWNER_ID"))
			if web.PasswordAccountID == "" {
				return Config{}, errors.New("EGGY_OWNER_ID must name which EGGY_ACCOUNTS entry the EGGY_UI_USER_EMAIL / EGGY_UI_PASSWORD credentials sign in")
			}
		}
		expected = normalizeEmail(getenv("EGGY_GOOGLE_EXPECTED_EMAIL"))
		ownerValue = ""
	} else if ownerValue != "" {
		ownerID, err := strconv.ParseInt(ownerValue, 10, 64)
		if err != nil || ownerID <= 0 {
			return Config{}, errors.New("EGGY_TELEGRAM_OWNER_ID must be a positive integer")
		}
		telegram = TelegramConfig{OwnerID: ownerID}
	} else {
		ownerValue = strings.TrimSpace(getenv("EGGY_OWNER_ID"))
		if ownerValue == "" {
			return Config{}, errors.New("EGGY_ACCOUNTS is required, or EGGY_TELEGRAM_OWNER_ID / EGGY_OWNER_ID for a single-owner deployment")
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
		DataDir:  homeDir,
		Owner:    OwnerConfig{ID: ownerValue},
		Telegram: telegram,
		Accounts: accounts,
		Web:      web,
		Google:   GoogleConfig{ExpectedEmail: expected},
		Agent:    AgentConfig{DefaultModel: "deepseek-pro", Timezone: "Asia/Singapore"},
		Providers: map[string]ProviderConfig{
			"deepseek": {Adapter: "openai_compatible", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY"},
		},
		ModelAliases: map[string]ModelAliasConfig{
			"deepseek-pro": {Provider: "deepseek", Model: "deepseek-v4-pro"},
		},
		Repositories: []RepositoryConfig{},
		Runner: RunnerConfig{
			Root:           filepath.Join(homeDir, "runs"),
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
