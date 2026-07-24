package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func handleConfigUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	return usageHelp(mustEntry("config"), fmt.Sprintf("Unknown config subcommand %q. Use get, set, or show.", firstOrEmpty(req.Args))), nil
}

func handleConfigGetUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	problem := "Expected exactly one section: providers, models, calendar, or path."
	if len(req.Args) > 0 {
		problem = fmt.Sprintf("Unknown config section %q. Use providers, models, calendar, or path.", req.Args[0])
	}
	return usageHelp(mustEntry("config get"), problem), nil
}

func handleConfigSetUsage(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	problem := "Expected exactly one section: provider, model, or calendar."
	if len(req.Args) > 0 {
		problem = fmt.Sprintf("Unknown config section %q. Use provider, model, or calendar.", req.Args[0])
	}
	return usageHelp(mustEntry("config set"), problem), nil
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func handleConfigGetProviders(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return CommandResult{State: ResultInfo, Title: "No providers configured.", Next: []string{"/config set provider name=<name> adapter=openai_compatible base_url=<url> api_key_env=<ENV_NAME>"}}, nil
	}
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		provider := cfg.Providers[name]
		rows = append(rows, []string{name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv})
	}
	return CommandResult{
		TableHeaders: []string{"Provider", "Adapter", "Base URL", "API key env"},
		TableRows:    rows,
		Detail:       "api_key_env names the environment variable holding the key; it is a reference, not the secret value itself.",
	}, nil
}

func handleConfigGetModels(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	aliases := make([]string, 0, len(cfg.ModelAliases))
	for alias := range cfg.ModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	if len(aliases) == 0 {
		return CommandResult{State: ResultInfo, Title: "No models configured.", Next: []string{"/config set model alias=<alias> provider=<provider> model=<model_id>"}}, nil
	}
	rows := make([][]string, 0, len(aliases))
	for _, alias := range aliases {
		model := cfg.ModelAliases[alias]
		efforts := "—"
		if len(model.ReasoningEfforts) > 0 {
			efforts = strings.Join(model.ReasoningEfforts, ", ")
		}
		rows = append(rows, []string{alias, model.Provider, model.Model, efforts})
	}
	return CommandResult{TableHeaders: []string{"Alias", "Provider", "Model", "Reasoning efforts"}, TableRows: rows}, nil
}

func handleConfigGetCalendar(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	cfg, err := loadConfigDocument(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Fields: []ResultField{
		{Label: "Enabled", Value: fmt.Sprintf("%t", cfg.Calendar.Enabled)},
		{Label: "Default calendar", Value: cfg.Calendar.DefaultCalendar},
		{Label: "Timezone", Value: cfg.Calendar.Timezone},
	}}, nil
}

func handleConfigGetPath(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	return CommandResult{Title: s.configPath}, nil
}

func handleConfigShow(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	text, err := ShowConfigText(s.configPath)
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Detail: text}, nil
}

func handleConfigSetProvider(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	name, adapter, baseURL, apiKeyEnv := req.Named["name"], req.Named["adapter"], req.Named["base_url"], req.Named["api_key_env"]
	if name == "" || adapter == "" || baseURL == "" || apiKeyEnv == "" {
		return usageHelp(mustEntry("config set provider"), "Required: name, adapter, base_url, api_key_env."), nil
	}
	if err := SetProvider(s.configPath, name, adapter, baseURL, apiKeyEnv); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set provider " + name + ".",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}

func handleConfigSetModel(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	alias, provider, modelID, reasoningEfforts := req.Named["alias"], req.Named["provider"], req.Named["model"], req.Named["reasoning_efforts"]
	if alias == "" || provider == "" || modelID == "" {
		return usageHelp(mustEntry("config set model"), "Required: alias, provider, model. Optional: reasoning_efforts (comma-separated)."), nil
	}
	if err := SetModelAlias(s.configPath, alias, provider, modelID, reasoningEfforts); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set model " + alias + ".",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}

func handleConfigSetCalendar(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.configPath == "" {
		return CommandResult{State: ResultInfo, Title: "Config file management is not configured."}, nil
	}
	enabled, defaultCalendar, timezone := req.Named["enabled"], req.Named["default_calendar"], req.Named["timezone"]
	for key := range req.Named {
		if key != "enabled" && key != "default_calendar" && key != "timezone" {
			return usageHelp(mustEntry("config set calendar"), fmt.Sprintf("Unknown field %q. Use enabled, default_calendar, or timezone.", key)), nil
		}
	}
	if enabled == "" && defaultCalendar == "" && timezone == "" {
		return usageHelp(mustEntry("config set calendar"), "At least one of enabled, default_calendar, or timezone is required."), nil
	}
	if err := SetCalendar(s.configPath, enabled, defaultCalendar, timezone); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  "Set calendar.",
		Detail: "Restart Eggy for this to take effect.",
		Next:   []string{"/restart"},
	}, nil
}
