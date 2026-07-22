package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"gopkg.in/yaml.v3"
)

func loadConfigDocument(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	var document configDocument
	if err := decodeKnownYAML(data, &document); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg := normalizeConfig(document)
	if err := cfg.applyDefaults(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// writeConfigUnlocked persists cfg atomically. Callers must hold the path's
// filelock for the whole load-mutate-write sequence, not just this step, or
// concurrent writers can race: both read the old file, both mutate their own
// copy, and the second write silently discards the first writer's change.
func writeConfigUnlocked(path string, cfg Config) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("persist config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	return os.Rename(temporaryPath, path)
}

func SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]ProviderConfig{}
		}
		cfg.Providers[name] = ProviderConfig{Adapter: adapter, BaseURL: baseURL, APIKeyEnv: apiKeyEnv}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetModelAlias configures alias. reasoningEfforts is a comma-separated list
// of supported levels (e.g. "low,medium,high,max"); pass "" to leave the
// alias without a reasoning-effort option.
func SetModelAlias(path, alias, provider, modelID, reasoningEfforts string) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if cfg.ModelAliases == nil {
			cfg.ModelAliases = map[string]ModelAliasConfig{}
		}
		var efforts []string
		if reasoningEfforts != "" {
			efforts = strings.Split(reasoningEfforts, ",")
		}
		cfg.ModelAliases[alias] = ModelAliasConfig{Provider: provider, Model: modelID, ReasoningEfforts: efforts}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetCalendar patches CalendarConfig field-by-field: an empty string for
// enabled, defaultCalendar, or timezone means "leave that field unchanged."
// At least one must be non-empty.
func SetCalendar(path, enabled, defaultCalendar, timezone string) error {
	if enabled == "" && defaultCalendar == "" && timezone == "" {
		return errors.New("at least one of enabled, default_calendar, or timezone is required")
	}
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if enabled != "" {
			parsed, err := strconv.ParseBool(enabled)
			if err != nil {
				return fmt.Errorf("enabled must be true or false: %w", err)
			}
			cfg.Calendar.Enabled = parsed
		}
		if defaultCalendar != "" {
			cfg.Calendar.DefaultCalendar = defaultCalendar
		}
		if timezone != "" {
			cfg.Calendar.Timezone = timezone
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetMCPServer upserts one MCP server definition by its essential,
// web-editable fields (url, auth, bearer_token_env, enabled). Transport is
// always "streamable-http" -- the only supported value -- so it is not
// user-facing. Advanced fields not exposed by the web form (oauth_scopes,
// tool_filter, timeouts) are preserved untouched when editing an existing
// server; a brand-new server gets the same sane defaults
// Config.applyDefaults would give it, so it validates immediately instead of
// only becoming valid after the next config load.
func SetMCPServer(path, name, url, auth, bearerTokenEnv string, enabled bool) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if cfg.MCP.Servers == nil {
			cfg.MCP.Servers = map[string]MCPServerConfig{}
		}
		server := cfg.MCP.Servers[name]
		server.URL = url
		if server.Transport == "" {
			server.Transport = "streamable-http"
		}
		server.Auth = auth
		server.BearerTokenEnv = bearerTokenEnv
		server.Enabled = enabled
		if server.ConnectTimeout == 0 {
			server.ConnectTimeout = Duration(10 * time.Second)
		}
		if server.Timeout == 0 {
			server.Timeout = Duration(time.Minute)
		}
		if server.MaxOutputBytes == 0 {
			server.MaxOutputBytes = 128 << 10
		}
		cfg.MCP.Servers[name] = server
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// RemoveMCPServer deletes one MCP server's config entry. It does not touch
// any already-persisted OAuth credentials under
// <data_dir>/mcp/<name>/oauth.json; use /mcp logout first if those should be
// cleared too.
func RemoveMCPServer(path, name string) error {
	return filelock.With(path, func() error {
		cfg, err := loadConfigDocument(path)
		if err != nil {
			return err
		}
		if _, ok := cfg.MCP.Servers[name]; !ok {
			return fmt.Errorf("MCP server %q is not configured", name)
		}
		delete(cfg.MCP.Servers, name)
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// GetMCPServersConfig returns the configured MCP servers keyed by name.
func GetMCPServersConfig(path string) (map[string]MCPServerConfig, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return nil, err
	}
	return cfg.MCP.Servers, nil
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No providers configured.", nil
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		lines = append(lines, fmt.Sprintf("%s  adapter=%s  base_url=%s  api_key_env=%s", name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv))
	}
	return strings.Join(lines, "\n"), nil
}

func GetModelAliasesConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		return "No models configured.", nil
	}
	lines := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		model := cfg.ModelAliases[alias]
		line := fmt.Sprintf("%s  provider=%s  model=%s", alias, model.Provider, model.Model)
		if len(model.ReasoningEfforts) > 0 {
			line += "  reasoning_efforts=" + strings.Join(model.ReasoningEfforts, ",")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func GetCalendarConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("enabled=%t  default_calendar=%s  timezone=%s", cfg.Calendar.Enabled, cfg.Calendar.DefaultCalendar, cfg.Calendar.Timezone), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, err := loadConfigDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
