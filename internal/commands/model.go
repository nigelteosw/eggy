// /model: which alias a turn runs on, and the browsing that makes writing a new
// alias possible without copying an ID out of a vendor's web page. Listing a
// model does not enable it -- the models section still governs.
package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ModelDiscoverer is the listing side of the configured providers, as /model
// needs it. It mirrors the web panel's interface of the same shape rather than
// sharing one: both are two-method views onto bootstrap's discovery, and a
// shared interface would only put internal/commands and internal/web in each
// other's import path to save four lines.
type ModelDiscoverer interface {
	DiscoverableProviders() []string
	DiscoverModels(ctx context.Context, provider string) ([]ports.CatalogModel, error)
}

func (s *CommandService) model(ctx context.Context, args []string) (string, bool, error) {
	if s.AgentRuntime == nil {
		return "Model selection is unavailable.", true, nil
	}
	if len(args) == 0 {
		selected, err := s.AgentRuntime.SelectedModel(ctx)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("**Active model:** %s\n**Available:** %s", selected, strings.Join(s.ModelAliases, ", ")), true, nil
	}
	// A subcommand only wins when no configured alias answers to that name, so
	// adding these words cannot make an existing alias unselectable.
	if _, taken := s.aliasSet[args[0]]; !taken {
		switch args[0] {
		case "providers":
			return s.modelProviders(), true, nil
		case "available":
			return s.modelAvailable(ctx, args[1:]), true, nil
		case "add":
			return s.modelAdd(args[1:]), true, nil
		}
	}
	if len(args) != 1 {
		return "Usage: /model <alias> | /model providers | /model available <provider> [filter] | /model add <alias> <provider> <model>", true, nil
	}
	alias := args[0]
	if alias == "default" {
		alias = ""
	}
	if err := s.AgentRuntime.SelectModel(ctx, alias); err != nil {
		return fmt.Sprintf("Could not select model: %v\n\nAvailable: %s", err, strings.Join(s.ModelAliases, ", ")), true, nil
	}
	if alias == "" {
		alias = s.DefaultModel
	}
	return "Model set to " + alias + ".", true, nil
}

// modelProviders names the providers a catalog can be asked for. It reports
// the ones that cannot be browsed too, because "openrouter is missing from
// this list" is the question an owner actually has, and silence answers it
// badly.
func (s *CommandService) modelProviders() string {
	names, err := config.ProviderNames(s.ConfigPath)
	if err != nil {
		return fmt.Sprintf("Could not read providers: %v", err)
	}
	if len(names) == 0 {
		return "No providers configured."
	}
	browsable := map[string]bool{}
	if s.ModelDiscovery != nil {
		for _, name := range s.ModelDiscovery.DiscoverableProviders() {
			browsable[name] = true
		}
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		note := "cannot be browsed"
		if browsable[name] {
			note = "browsable -- /model available " + name
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", name, note))
	}
	return "**Providers:**\n" + strings.Join(lines, "\n")
}

// modelAvailable browses one provider's catalog. The filter argument is not a
// convenience: OpenRouter answers with several hundred entries, and a chat
// surface that dumped all of them would be unusable, so an unfiltered listing
// is capped and says so rather than being silently cut.
func (s *CommandService) modelAvailable(ctx context.Context, args []string) string {
	if s.ModelDiscovery == nil {
		return "Model discovery is unavailable."
	}
	if len(args) == 0 {
		return "Usage: /model available <provider> [filter]\n\n" + s.modelProviders()
	}
	provider := args[0]
	filter := strings.ToLower(strings.Join(args[1:], " "))
	models, err := s.ModelDiscovery.DiscoverModels(ctx, provider)
	if err != nil {
		return fmt.Sprintf("Could not list %s models: %v", provider, err)
	}
	matched := make([]string, 0, len(models))
	for _, model := range models {
		if filter != "" && !strings.Contains(strings.ToLower(model.ID), filter) && !strings.Contains(strings.ToLower(model.Name), filter) {
			continue
		}
		matched = append(matched, model.ID)
	}
	if len(matched) == 0 {
		if filter != "" {
			return fmt.Sprintf("No %s model matches %q.", provider, filter)
		}
		return provider + " reported no models."
	}
	slices.Sort(matched)
	const cap = 40
	header := fmt.Sprintf("**%s models** (%d", provider, len(matched))
	if filter != "" {
		header += fmt.Sprintf(" matching %q", filter)
	}
	header += ")"
	truncated := ""
	if len(matched) > cap {
		truncated = fmt.Sprintf("\n\n...and %d more. Narrow it with /model available %s <filter>.", len(matched)-cap, provider)
		matched = matched[:cap]
	}
	// The reminder is the point of the whole listing: seeing a model here is
	// not the same as being able to run it.
	return header + "\n" + strings.Join(matched, "\n") +
		truncated + "\n\nAdd one with /model add <alias> " + provider + " <model>."
}

// modelAdd writes an alias. It is the one command here that changes config,
// and it deliberately does not select the new alias: the running daemon is
// still holding the old catalog, so selecting it would fail on an alias the
// owner can see in config.yaml, which is worse than being told to restart.
func (s *CommandService) modelAdd(args []string) string {
	if len(args) < 3 || len(args) > 4 {
		return "Usage: /model add <alias> <provider> <model> [efforts]\n\nefforts is an optional comma-separated list such as low,medium,high."
	}
	alias, provider, modelID := args[0], args[1], args[2]
	efforts := ""
	if len(args) == 4 {
		efforts = args[3]
	}
	if err := config.SetModelAlias(s.ConfigPath, alias, provider, modelID, efforts); err != nil {
		return fmt.Sprintf("Could not add %s: %v", alias, err)
	}
	return fmt.Sprintf("Added **%s** -> %s %s.\n\nRestart for it to become selectable: /restart", alias, provider, modelID)
}
