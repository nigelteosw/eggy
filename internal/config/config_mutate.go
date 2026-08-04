package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/plugins/filelock"
	"gopkg.in/yaml.v3"
)

// LoadDocument reads the config file as written and applies defaults, without
// the environment and secret overrides LoadConfig layers on top. Callers that
// display or mutate the stored document want this; callers that need the
// effective runtime configuration want LoadConfig.
func LoadDocument(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	var cfg Config
	if err := decodeKnownYAML(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
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
	return writeFileAtomic(path, body)
}

// writeFileAtomic replaces path with body through a same-directory temporary
// file, so a crash mid-write leaves the previous config intact rather than a
// truncated one the next start cannot parse.
func writeFileAtomic(path string, body []byte) error {
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

// ReplaceConfig writes body over the config file, but only once body has been
// proved loadable. Validation runs against a temporary file through LoadConfig
// itself rather than a reimplementation of it, so what is accepted here is
// exactly what the next start accepts -- including the environment overrides
// and required secrets, which a plain decode would not catch.
//
// The owner's bytes are written verbatim: this is the repair path for a config
// the daemon could not parse, and re-marshalling would throw away the comments
// and ordering of the file they are trying to fix.
func ReplaceConfig(path string, body []byte, getenv func(string) string) error {
	return filelock.With(path, func() error {
		candidate, err := os.CreateTemp(filepath.Dir(path), ".config-candidate-*.tmp")
		if err != nil {
			return fmt.Errorf("stage config: %w", err)
		}
		candidatePath := candidate.Name()
		defer os.Remove(candidatePath)
		if err := candidate.Chmod(0o600); err != nil {
			candidate.Close()
			return fmt.Errorf("stage config: %w", err)
		}
		if _, err := candidate.Write(body); err != nil {
			candidate.Close()
			return fmt.Errorf("stage config: %w", err)
		}
		if err := candidate.Close(); err != nil {
			return fmt.Errorf("stage config: %w", err)
		}
		if _, _, err := LoadConfig(candidatePath, getenv); err != nil {
			return err
		}
		return writeFileAtomic(path, body)
	})
}

func SetProvider(path, name, adapter, baseURL, apiKeyEnv string) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
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
		cfg, err := LoadDocument(path)
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

// MCPServerInput is the set of MCP server fields an owner can set from a
// surface -- the web settings panel or the Telegram /mcp command. It is a
// struct rather than a parameter list because transport made it the seventh
// positional string, and a run of same-typed positional strings is how a URL
// ends up in the auth field.
type MCPServerInput struct {
	Name                 string
	URL                  string
	Transport            string
	Auth                 string
	BearerTokenEnv       string
	OAuthClientID        string
	OAuthClientSecretEnv string
	Enabled              bool
}

// SetMCPServer upserts one MCP server definition from the fields a surface can
// set. An empty Transport keeps whatever the server already had and defaults a
// new server to streamable-http. A stdio server is a local subprocess with a
// command line and an environment allowlist, which belongs in reviewed
// configuration rather than a chat message or a web form: writing one is
// refused here in either direction rather than silently rewritten into an HTTP
// server. Advanced fields no surface exposes (oauth_scopes, tool_filter,
// timeouts) are preserved untouched when editing an existing server; a
// brand-new server gets the same sane defaults Config.applyDefaults would give
// it, so it validates immediately instead of only becoming valid after the
// next config load.
func SetMCPServer(path string, input MCPServerInput) error {
	name := input.Name
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		if cfg.MCP.Servers == nil {
			cfg.MCP.Servers = map[string]MCPServerConfig{}
		}
		server := cfg.MCP.Servers[name]
		if server.Transport == "stdio" || input.Transport == "stdio" {
			return fmt.Errorf("MCP server %q uses the stdio transport; edit it in config.yaml", name)
		}
		server.URL = input.URL
		if input.Transport != "" {
			server.Transport = input.Transport
		}
		if server.Transport == "" {
			server.Transport = "streamable-http"
		}
		server.Auth = input.Auth
		server.BearerTokenEnv = input.BearerTokenEnv
		// An omitted client id keeps whatever the server already had, so
		// editing a URL does not silently drop a hand-registered client. A
		// stored value that no longer validates is the exception: keeping it
		// would make every later edit fail on a field the owner cannot see or
		// clear from any surface, so an unusable one is dropped instead.
		if input.OAuthClientID != "" {
			server.OAuthClientID = input.OAuthClientID
		}
		switch {
		case input.OAuthClientSecretEnv != "":
			server.OAuthClientSecretEnv = input.OAuthClientSecretEnv
		case !environmentNamePattern.MatchString(server.OAuthClientSecretEnv):
			server.OAuthClientSecretEnv = ""
		}
		if server.OAuthClientID == "" {
			// The secret env is meaningless without the client it belongs to,
			// and validation rejects the pair, so clearing one clears both.
			server.OAuthClientSecretEnv = ""
		}
		server.Enabled = input.Enabled
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

// SetMCPServerEnabled flips one server's enabled flag without touching
// anything else about it. It is the one MCP edit a stdio server also accepts:
// turning a subprocess server off needs no knowledge of its command or args,
// so refusing it here would force a file edit for the safest possible change.
func SetMCPServerEnabled(path, name string, enabled bool) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		server, ok := cfg.MCP.Servers[name]
		if !ok {
			return fmt.Errorf("MCP server %q is not configured", name)
		}
		server.Enabled = enabled
		cfg.MCP.Servers[name] = server
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// RemoveMCPServer deletes one MCP server's config entry. Persisted OAuth
// credentials are retained so re-adding the server does not silently revoke
// its authorization.
func RemoveMCPServer(path, name string) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
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
	cfg, err := LoadDocument(path)
	if err != nil {
		return nil, err
	}
	return cfg.MCP.Servers, nil
}

// GoogleInput is the set of Google fields a surface can set. Scopes, timeout,
// and max_output_bytes are deliberately absent: narrowing a consent screen and
// bounding a response are reviewed decisions, and both are preserved untouched
// when a surface edits the rest.
type GoogleInput struct {
	Enabled         bool
	ClientID        string
	ClientSecretEnv string
	Products        []string
	// RequireApproval carries three states through one pointer, because the
	// setting itself has three and collapsing any two would silently change
	// what asks. Nil leaves the stored list alone, like every other field
	// here. A pointer to a nil slice removes the key, so each tool's own
	// classification decides -- including actions a later version adds. A
	// pointer to a list replaces it, and a pointer to an empty list is the
	// owner saying nothing should ask.
	RequireApproval *[]string
}

// SetGoogle writes the Google section from the fields a surface can set.
//
// It is upsert-shaped like SetMCPServer for the same reason: the owner editing
// from a phone or a browser is amending one integration, not authoring the
// section, so anything they cannot see must survive the write. Validation runs
// before the file is touched, so an unknown product name or a client id left
// blank is rejected with the existing config still in place.
func SetGoogle(path string, input GoogleInput) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		google := cfg.Google
		google.Enabled = input.Enabled
		if input.ClientID != "" {
			google.ClientID = input.ClientID
		}
		if input.ClientSecretEnv != "" {
			google.ClientSecretEnv = input.ClientSecretEnv
		}
		if len(input.Products) > 0 {
			// Sorted on the way in so the written file has a stable order a
			// surface can round-trip; the spelling rule itself is shared.
			products := normalizeProducts(input.Products)
			slices.Sort(products)
			google.Products = products
		}
		if input.RequireApproval != nil {
			// A pointer to a nil slice is the request to remove the key, so it
			// is stored as no pointer at all rather than as an empty list.
			if *input.RequireApproval == nil {
				google.RequireApproval = nil
			} else {
				google.RequireApproval = input.RequireApproval
			}
		}
		cfg.Google = google
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetAppearance writes the owner's web panel theme.
//
// The theme is validated against the two names the stylesheet defines rather
// than stored as free text: an unrecognised value would render as neither
// theme, and the failure would surface as an unstyled page instead of as a
// rejected form.
func SetAppearance(path, theme string) error {
	trimmed := strings.TrimSpace(theme)
	if trimmed != ThemeDark && trimmed != ThemeLight {
		return fmt.Errorf("appearance.theme must be %q or %q", ThemeDark, ThemeLight)
	}
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		cfg.Appearance.Theme = trimmed
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

// SetHeartbeat writes the heartbeat section from the fields a surface can set.
//
// An empty Interval means off, and off is spelled by writing a zero interval
// rather than by leaving the section as it was: turning the heartbeat off is
// the single most likely reason an owner opens this form, so a blank field
// that silently preserved the old interval would be the wrong default in
// exactly the case that matters. Instruction keeps the upsert shape the other
// setters use -- blank leaves the configured wording alone, since clearing it
// falls back to the built-in prompt anyway.
func SetHeartbeat(path, interval, instruction string) error {
	parsed := Duration(0)
	if trimmed := strings.TrimSpace(interval); trimmed != "" {
		value, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("heartbeat.interval: %w", err)
		}
		parsed = Duration(value)
	}
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		cfg.Heartbeat.Interval = parsed
		if trimmed := strings.TrimSpace(instruction); trimmed != "" {
			cfg.Heartbeat.Instruction = trimmed
		}
		// A heartbeat with nowhere to deliver is refused here rather than
		// saved and ignored, so the owner learns it at the form instead of
		// from a warning in a log they will not read.
		if parsed > 0 && !cfg.Telegram.Configured() {
			return errors.New("heartbeat needs a configured telegram channel, since that is where unprompted output goes")
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		return writeConfigUnlocked(path, cfg)
	})
}

func GetProvidersConfigText(path string) (string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	slices.Sort(names)
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
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	slices.Sort(aliases)
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

// ReadConfigText returns the config file exactly as stored, comments and all.
// Safe to expose to the authenticated owner for the same reason ShowConfigText
// is: config.yaml holds environment-variable names, never secret values.
func ReadConfigText(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("open config: %w", err)
	}
	return string(body), nil
}

// ShowConfigText re-marshals the whole config as YAML. Safe to expose in
// full: config.yaml never holds secret values, only environment-variable
// names (api_key_env, credential_env).
func ShowConfigText(path string) (string, error) {
	cfg, err := LoadDocument(path)
	if err != nil {
		return "", err
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(body), nil
}
