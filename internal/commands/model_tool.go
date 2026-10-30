// The model tool: /model, for the agent. An owner who says "add Gemini Flash
// Image from OpenRouter" or "switch to sonnet" gets the same alias write and the
// same selection /model performs, because the tool calls the same config helper
// and the same runtime rather than keeping a second path to either.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

var modelToolSchema = json.RawMessage(`{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["list","search","add","select"]},` +
	`"query":{"type":"string","description":"search: words from the model's name, such as \"gemini flash image\"."},` +
	`"model":{"type":"string","description":"add: the exact OpenRouter model ID that search returned."},` +
	`"alias":{"type":"string","description":"add: a short name for the new model, lowercase letters, digits and dashes. select: the alias to switch to, or \"default\"."},` +
	`"provider":{"type":"string","description":"add or search: the OpenRouter provider entry, only when more than one is configured."}},` +
	`"required":["action"],"additionalProperties":false}`)

// modelSearchLimit bounds one search. OpenRouter lists several hundred models
// and the answer goes into the context window.
const modelSearchLimit = 15

type modelTool struct{ s *CommandService }

// ModelTool is the agent's view of /model.
func (s *CommandService) ModelTool() ports.Tool { return modelTool{s: s} }

func (t modelTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name: "model",
		Description: "See and change which model answers this account. " +
			"list: the configured aliases and which one is active. " +
			"search: find OpenRouter models by name; returns exact IDs and whether each draws images. " +
			"add: add an OpenRouter model as a new alias -- search first and pass the exact ID; Eggy restarts to load it, after which it can be selected. " +
			"select: switch this account to a configured alias from its next message on; \"default\" goes back to the default.",
		Schema: modelToolSchema,
		// select writes only the calling account's own model choice, which
		// /model shows and undoes -- the same footing InternalTool gives
		// memory -- so it runs unasked in normal mode. add writes the shared
		// config.yaml and restarts Eggy for every account, so it asks.
		Effect: ports.MutatingActions("add"),
	}
}

type modelToolInput struct {
	Action   string `json:"action"`
	Query    string `json:"query"`
	Model    string `json:"model"`
	Alias    string `json:"alias"`
	Provider string `json:"provider"`
}

// DescribeCall puts the add on the approval card in words.
func (t modelTool) DescribeCall(raw json.RawMessage) string {
	var input modelToolInput
	if json.Unmarshal(raw, &input) != nil || input.Action != "add" {
		return ""
	}
	return fmt.Sprintf("Add OpenRouter model %s as the alias %q, then restart Eggy to load it.", input.Model, input.Alias)
}

func (t modelTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input modelToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decode model input: %w", err)
	}
	if t.s.AgentRuntime == nil {
		return nil, errors.New("model selection is unavailable")
	}
	switch input.Action {
	case "list":
		return t.list(ctx)
	case "search":
		return t.search(ctx, input)
	case "add":
		return t.add(ctx, input)
	case "select":
		return t.selectAlias(ctx, strings.TrimSpace(input.Alias))
	default:
		return nil, fmt.Errorf("unknown action %q", input.Action)
	}
}

type modelAliasEntry struct {
	Alias    string `json:"alias"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Active   bool   `json:"active,omitempty"`
	// RestartPending marks an alias written to config.yaml that this process
	// has not loaded, so selecting it would fail until Eggy restarts.
	RestartPending bool `json:"restart_pending,omitempty"`
}

func (t modelTool) list(ctx context.Context) (json.RawMessage, error) {
	active, err := t.s.AgentRuntime.SelectedModel(ctx)
	if err != nil {
		return nil, err
	}
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return nil, err
	}
	entries := make([]modelAliasEntry, 0, len(document.ModelAliases))
	for alias, configured := range document.ModelAliases {
		_, loaded := t.s.aliasSet[alias]
		entries = append(entries, modelAliasEntry{
			Alias: alias, Provider: configured.Provider, Model: configured.Model,
			Active: alias == active, RestartPending: !loaded,
		})
	}
	slices.SortFunc(entries, func(a, b modelAliasEntry) int { return strings.Compare(a.Alias, b.Alias) })
	return json.Marshal(struct {
		Active  string            `json:"active"`
		Default string            `json:"default"`
		Aliases []modelAliasEntry `json:"aliases"`
	}{active, t.s.DefaultModel, entries})
}

type modelSearchResult struct {
	Provider    string   `json:"provider"`
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	DrawsImages bool     `json:"draws_images,omitempty"`
	ReadsImages bool     `json:"reads_images,omitempty"`
	Efforts     []string `json:"reasoning_efforts,omitempty"`
	// Alias names the configured alias already pointing at this model.
	Alias string `json:"alias,omitempty"`
}

func (t modelTool) search(ctx context.Context, input modelToolInput) (json.RawMessage, error) {
	words := strings.Fields(strings.ToLower(input.Query))
	if len(words) == 0 {
		return nil, errors.New("search needs a query")
	}
	document, providers, err := t.openRouterProviders(input.Provider)
	if err != nil {
		return nil, err
	}
	configured := map[string]string{}
	for alias, target := range document.ModelAliases {
		configured[target.Provider+"\x00"+target.Model] = alias
	}
	var results []modelSearchResult
	total := 0
	for _, provider := range providers {
		models, err := t.s.ModelDiscovery.DiscoverModels(ctx, provider)
		if err != nil {
			return nil, fmt.Errorf("list %s models: %w", provider, err)
		}
		for _, model := range models {
			haystack := strings.ToLower(model.ID + " " + model.Name)
			if !allWordsIn(haystack, words) {
				continue
			}
			total++
			if len(results) == modelSearchLimit {
				continue
			}
			result := modelSearchResult{
				Provider: provider, ID: model.ID, Name: model.Name,
				DrawsImages: model.Produces(ports.ModalityImage),
				Alias:       configured[provider+"\x00"+model.ID],
			}
			result.ReadsImages, _ = model.Accepts(ports.ModalityImage)
			if model.Reasoning != nil {
				result.Efforts = model.Reasoning.Efforts
			}
			results = append(results, result)
		}
	}
	return json.Marshal(struct {
		Matches   int                 `json:"matches"`
		Truncated bool                `json:"truncated,omitempty"`
		Results   []modelSearchResult `json:"results"`
	}{total, total > len(results), results})
}

// allWordsIn is the search match: every word the owner gave, anywhere in the
// ID or the display name, so "gemini flash image" finds
// google/gemini-2.5-flash-image without the owner knowing the vendor prefix.
func allWordsIn(haystack string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

func (t modelTool) add(ctx context.Context, input modelToolInput) (json.RawMessage, error) {
	alias, modelID := strings.TrimSpace(input.Alias), strings.TrimSpace(input.Model)
	if alias == "" || modelID == "" {
		return nil, errors.New("add needs an alias and the exact model ID from search")
	}
	document, providers, err := t.openRouterProviders(input.Provider)
	if err != nil {
		return nil, err
	}
	if len(providers) > 1 {
		return nil, fmt.Errorf("more than one OpenRouter provider is configured (%s); name one as provider", strings.Join(providers, ", "))
	}
	provider := providers[0]
	if existing, taken := document.ModelAliases[alias]; taken {
		return nil, fmt.Errorf("alias %q already names %s %s; pick another name", alias, existing.Provider, existing.Model)
	}
	// The ID is checked against the catalog rather than trusted, so a model
	// that guessed an ID gets told to search instead of writing an alias
	// every turn on it would fail.
	models, err := t.s.ModelDiscovery.DiscoverModels(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("list %s models: %w", provider, err)
	}
	index := slices.IndexFunc(models, func(model ports.CatalogModel) bool { return model.ID == modelID })
	if index < 0 {
		return nil, fmt.Errorf("%s does not list a model with the ID %q; search for it and pass the exact ID", provider, modelID)
	}
	aliasInput := config.ModelAliasInput{Alias: alias, Provider: provider, Model: modelID}
	if reasoning := models[index].Reasoning; reasoning != nil {
		aliasInput.ReasoningEfforts = reasoning.Efforts
	}
	if err := config.SetModelAlias(t.s.ConfigPath, aliasInput); err != nil {
		return nil, err
	}
	added := fmt.Sprintf("Added %s -> %s %s.", alias, provider, modelID)
	restart, restarting := Restart(t.s.Restarter, t.s.ConfigPath, t.s.Getenv)
	if !restarting {
		return json.Marshal(map[string]string{"result": added + " It becomes selectable after a restart, which did not happen: " + restart})
	}
	return json.Marshal(map[string]string{"result": added + " Eggy is restarting to load it; once it is back, ask to switch to " + alias + "."})
}

func (t modelTool) selectAlias(ctx context.Context, alias string) (json.RawMessage, error) {
	if alias == "" {
		return nil, errors.New("select needs an alias")
	}
	if alias == "default" {
		alias = ""
	}
	if err := t.s.AgentRuntime.SelectModel(ctx, alias); err != nil {
		if _, onDisk := t.aliasOnDisk(alias); onDisk {
			return nil, fmt.Errorf("alias %q was added but Eggy has not restarted since; it can be selected after /restart", alias)
		}
		return nil, fmt.Errorf("%w; configured: %s", err, strings.Join(t.s.ModelAliases, ", "))
	}
	if alias == "" {
		alias = t.s.DefaultModel
	}
	return json.Marshal(map[string]string{"result": "Switched to " + alias + ". It answers from the owner's next message on; this reply is still from the previous model."})
}

func (t modelTool) aliasOnDisk(alias string) (config.ModelAliasConfig, bool) {
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return config.ModelAliasConfig{}, false
	}
	configured, ok := document.ModelAliases[alias]
	return configured, ok
}

// openRouterProviders is the configured OpenRouter providers that can be
// browsed, narrowed to one when the call named it.
func (t modelTool) openRouterProviders(named string) (config.Config, []string, error) {
	if t.s.ModelDiscovery == nil {
		return config.Config{}, nil, errors.New("model discovery is unavailable")
	}
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return config.Config{}, nil, err
	}
	var providers []string
	for _, name := range t.s.ModelDiscovery.DiscoverableProviders() {
		if !document.Providers[name].IsOpenRouter() {
			continue
		}
		if named != "" && named != name {
			continue
		}
		providers = append(providers, name)
	}
	if len(providers) == 0 {
		if named != "" {
			return config.Config{}, nil, fmt.Errorf("%q is not a browsable OpenRouter provider", named)
		}
		return config.Config{}, nil, errors.New("no browsable OpenRouter provider is configured")
	}
	slices.Sort(providers)
	return document, providers, nil
}
