package main

import (
	"flag"
	"fmt"

	"github.com/nigelteosw/eggy/internal/bootstrap"
)

func configMain(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config get <coding|providers|models|calendar|path>|show|set <coding-agent|provider|model|calendar> ..."
	if len(arguments) == 0 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "get":
		return configGet(configPath, arguments[1:])
	case "set":
		return configSet(configPath, arguments[1:])
	case "show":
		return bootstrap.ShowConfigText(configPath)
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configGet(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config get <coding|providers|models|calendar|path>"
	if len(arguments) != 1 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "coding":
		return bootstrap.GetCodingConfigText(configPath)
	case "providers":
		return bootstrap.GetProvidersConfigText(configPath)
	case "models":
		return bootstrap.GetModelAliasesConfigText(configPath)
	case "calendar":
		return bootstrap.GetCalendarConfigText(configPath)
	case "path":
		return configPath, nil
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configSet(configPath string, arguments []string) (string, error) {
	usage := "usage: eggy config set <coding-agent|provider|model|calendar> ..."
	if len(arguments) == 0 {
		return "", fmt.Errorf("%s", usage)
	}
	switch arguments[0] {
	case "coding-agent":
		return configSetCodingAgent(configPath, arguments[1:])
	case "provider":
		return configSetProvider(configPath, arguments[1:])
	case "model":
		return configSetModel(configPath, arguments[1:])
	case "calendar":
		return configSetCalendar(configPath, arguments[1:])
	default:
		return "", fmt.Errorf("%s", usage)
	}
}

func configSetCodingAgent(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set coding-agent", flag.ContinueOnError)
	alias := flags.String("alias", "", "coding agent alias")
	adapter := flags.String("adapter", "", "adapter: codex_cli or claude_cli")
	credentialEnv := flags.String("credential-env", "", "environment variable name holding the credential (optional)")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *alias == "" || *adapter == "" {
		return "", fmt.Errorf("usage: eggy config set coding-agent --alias=<alias> --adapter=<codex_cli|claude_cli> [--credential-env=<ENV_NAME>]")
	}
	if err := bootstrap.SetCodingAgent(configPath, *alias, *adapter, *credentialEnv); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set coding agent %s. Restart Eggy for this to take effect.", *alias), nil
}

func configSetProvider(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set provider", flag.ContinueOnError)
	name := flags.String("name", "", "provider name")
	adapter := flags.String("adapter", "", "adapter: openai_compatible")
	baseURL := flags.String("base-url", "", "provider base URL")
	apiKeyEnv := flags.String("api-key-env", "", "environment variable name holding the API key")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *name == "" || *adapter == "" || *baseURL == "" || *apiKeyEnv == "" {
		return "", fmt.Errorf("usage: eggy config set provider --name=<name> --adapter=openai_compatible --base-url=<url> --api-key-env=<ENV_NAME>")
	}
	if err := bootstrap.SetProvider(configPath, *name, *adapter, *baseURL, *apiKeyEnv); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set provider %s. Restart Eggy for this to take effect.", *name), nil
}

func configSetModel(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set model", flag.ContinueOnError)
	alias := flags.String("alias", "", "model alias")
	provider := flags.String("provider", "", "provider name")
	model := flags.String("model", "", "model ID")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if *alias == "" || *provider == "" || *model == "" {
		return "", fmt.Errorf("usage: eggy config set model --alias=<alias> --provider=<provider> --model=<model_id>")
	}
	if err := bootstrap.SetModelAlias(configPath, *alias, *provider, *model); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set model %s. Restart Eggy for this to take effect.", *alias), nil
}

func configSetCalendar(configPath string, arguments []string) (string, error) {
	flags := flag.NewFlagSet("config set calendar", flag.ContinueOnError)
	enabled := flags.String("enabled", "", "true or false")
	defaultCalendar := flags.String("default-calendar", "", "default calendar ID")
	timezone := flags.String("timezone", "", "IANA timezone")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if err := bootstrap.SetCalendar(configPath, *enabled, *defaultCalendar, *timezone); err != nil {
		return "", err
	}
	return "Set calendar. Restart Eggy for this to take effect.", nil
}
