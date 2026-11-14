// The settings tool: Eggy's view of its own settings. An owner who says "think
// harder", "show me your thinking", "check in on me every two hours" or "add
// Gemini Flash Image from OpenRouter" gets the same write /model, /mode,
// /heartbeat and the web panel perform, because every action here calls the
// same runtime and the same internal/config helper rather than keeping a
// second path to any of them. show is how Eggy knows itself: every setting,
// its value, and where a setting this tool does not change is changed.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

var settingsToolSchema = json.RawMessage(`{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["show","list_models","search_models","add_model","select_model","set_effort","set_thinking","set_mode","set_checkins","set_checkin_schedule","show_theme","set_theme"]},` +
	`"query":{"type":"string","description":"search_models: words from the model's name, such as \"gemini flash image\"."},` +
	`"model":{"type":"string","description":"add_model: the exact OpenRouter model ID that search_models returned."},` +
	`"alias":{"type":"string","description":"add_model: a short name for the new model, lowercase letters, digits and dashes. select_model: the alias to switch to, or \"default\"."},` +
	`"provider":{"type":"string","description":"add_model or search_models: the OpenRouter provider entry, only when more than one is configured."},` +
	`"effort":{"type":"string","description":"set_effort: one of the active model's efforts that show lists, or \"default\"."},` +
	`"on":{"type":"boolean","description":"set_thinking: show the model's reasoning. set_checkins: check in on this person."},` +
	`"mode":{"type":"string","enum":["strict","normal"],"description":"set_mode: strict asks before every tool, normal before anything that changes or sends."},` +
	`"interval":{"type":"string","description":"set_checkin_schedule: how often, such as \"2h\" or \"30m\", or \"off\". Omit to keep it."},` +
	`"active_hours":{"type":"string","description":"set_checkin_schedule: \"08:00-22:00\" in the deployment's timezone, or \"always\". Omit to keep it."},` +
	`"instruction":{"type":"string","description":"set_checkin_schedule: what to look for on each check-in. Omit to keep it."},` +
	`"scheme":{"type":"string","enum":["system","light","dark"],"description":"set_theme: light or dark mode, or follow the device."},` +
	`"look":{"type":"string","enum":["eggshell","cast-iron","custom"],"description":"set_theme: a built-in look, or custom to go back to the stored custom one."},` +
	`"light":{"type":"object","additionalProperties":{"type":"string"},"description":"set_theme: a custom look's light-mode colours, token name to \"H S% L%\". Replaces the stored custom look and selects it."},` +
	`"dark":{"type":"object","additionalProperties":{"type":"string"},"description":"set_theme: as light, for dark mode."},` +
	`"text_font":{"type":"string","description":"set_theme: one of the text fonts show_theme lists."},` +
	`"code_font":{"type":"string","description":"set_theme: one of the code fonts show_theme lists."}},` +
	`"required":["action"],"additionalProperties":false}`)

// modelSearchLimit bounds one search. OpenRouter lists several hundred models
// and the answer goes into the context window.
const modelSearchLimit = 15

type settingsTool struct{ s *CommandService }

// SettingsTool is the agent's view of its own settings.
func (s *CommandService) SettingsTool() ports.Tool { return settingsTool{s: s} }

func (t settingsTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name: "settings",
		Description: "See and change your own settings. " +
			"show: every setting with its current value -- this person's model, effort, thinking, when you ask, check-ins, and the shared ones -- plus what is connected and where each other setting is changed. " +
			"list_models: the configured model aliases and which is active. " +
			"search_models: find OpenRouter models by name; returns exact IDs and whether each draws images. " +
			"add_model: add an OpenRouter model as a new alias -- search first and pass the exact ID; Eggy restarts to load it. " +
			"select_model: switch this person to a configured alias from their next message on; \"default\" goes back to the default. " +
			"set_effort: how hard the active model reasons. set_thinking: whether this person sees the model's reasoning. " +
			"set_mode: when you ask before using a tool, strict or normal; only the person can choose never asking, themselves, with /mode auto or in Settings. " +
			"set_checkins: whether you check in on this person, only when they ask for it. " +
			"set_checkin_schedule: how often check-ins run, between which hours, and what they look for -- shared by everyone; Eggy restarts to apply it. " +
			"show_theme: this person's web panel look -- light or dark mode, look, fonts, and every colour token with its default -- read it before designing a custom look. " +
			"set_theme: change any of those for this person only; light and dark colours make a custom look, refused if text would be hard to read.",
		Schema: settingsToolSchema,
		// The personal settings write only the calling account's own
		// choices, which /model and the panel show and undo -- the same
		// footing InternalTool gives memory -- so they run unasked in
		// normal mode. How much the person is asked, and whether Eggy may
		// message them unprompted, are theirs to decide on a card rather
		// than a model's to decide in passing, so those ask. add_model and
		// set_checkin_schedule write the shared config.yaml and restart Eggy
		// for every account, so they ask too.
		Effect: ports.MutatingActions("add_model", "set_mode", "set_checkins", "set_checkin_schedule"),
	}
}

type settingsToolInput struct {
	Action      string            `json:"action"`
	Query       string            `json:"query"`
	Model       string            `json:"model"`
	Alias       string            `json:"alias"`
	Provider    string            `json:"provider"`
	Effort      string            `json:"effort"`
	On          *bool             `json:"on"`
	Mode        string            `json:"mode"`
	Interval    *string           `json:"interval"`
	ActiveHours *string           `json:"active_hours"`
	Instruction *string           `json:"instruction"`
	Scheme      *string           `json:"scheme"`
	Look        *string           `json:"look"`
	Light       map[string]string `json:"light"`
	Dark        map[string]string `json:"dark"`
	TextFont    *string           `json:"text_font"`
	CodeFont    *string           `json:"code_font"`
}

// DescribeCall puts each asking action on the approval card in words.
func (t settingsTool) DescribeCall(raw json.RawMessage) string {
	var input settingsToolInput
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch input.Action {
	case "add_model":
		return fmt.Sprintf("Add OpenRouter model %s as the alias %q, then restart Eggy to load it.", input.Model, input.Alias)
	case "set_mode":
		mode := ports.ApprovalMode(input.Mode)
		if !mode.Valid() || mode == ports.ModeAuto {
			return ""
		}
		return "Change when I ask to " + ModeLabel(mode) + ". " + mode.Describe()
	case "set_checkins":
		if input.On == nil {
			return ""
		}
		if *input.On {
			return "Turn check-ins on for you, so I can message you unprompted when something on your watch list needs you."
		}
		return "Turn your check-ins off."
	case "set_checkin_schedule":
		parts := []string{}
		if input.Interval != nil {
			if isOff(*input.Interval) {
				parts = append(parts, "turn check-ins off for everyone")
			} else {
				parts = append(parts, "check in every "+strings.TrimSpace(*input.Interval))
			}
		}
		if input.ActiveHours != nil {
			if isAlways(*input.ActiveHours) {
				parts = append(parts, "at any hour")
			} else {
				parts = append(parts, "only between "+strings.TrimSpace(*input.ActiveHours))
			}
		}
		if input.Instruction != nil {
			parts = append(parts, fmt.Sprintf("look for: %q", strings.TrimSpace(*input.Instruction)))
		}
		if len(parts) == 0 {
			return ""
		}
		return "For everyone who uses Eggy: " + strings.Join(parts, "; ") + ". Then restart Eggy to apply it."
	}
	return ""
}

func (t settingsTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input settingsToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decode settings input: %w", err)
	}
	if t.s.AgentRuntime == nil {
		return nil, errors.New("settings are unavailable")
	}
	switch input.Action {
	case "show":
		return t.show(ctx)
	case "list_models":
		return t.list(ctx)
	case "search_models":
		return t.search(ctx, input)
	case "add_model":
		return t.add(ctx, input)
	case "select_model":
		return t.selectAlias(ctx, strings.TrimSpace(input.Alias))
	case "set_effort":
		return t.setEffort(ctx, strings.TrimSpace(input.Effort))
	case "set_thinking":
		return t.setThinking(ctx, input.On)
	case "set_mode":
		return t.setMode(ctx, input.Mode)
	case "set_checkins":
		return t.setCheckins(ctx, input.On)
	case "set_checkin_schedule":
		return t.setCheckinSchedule(input)
	case "show_theme":
		return t.showTheme(ctx)
	case "set_theme":
		return t.setTheme(ctx, input)
	default:
		return nil, fmt.Errorf("unknown action %q", input.Action)
	}
}

func result(text string) (json.RawMessage, error) {
	return json.Marshal(map[string]string{"result": text})
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

func (t settingsTool) list(ctx context.Context) (json.RawMessage, error) {
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

func (t settingsTool) search(ctx context.Context, input settingsToolInput) (json.RawMessage, error) {
	words := strings.Fields(strings.ToLower(input.Query))
	if len(words) == 0 {
		return nil, errors.New("search_models needs a query")
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

func (t settingsTool) add(ctx context.Context, input settingsToolInput) (json.RawMessage, error) {
	alias, modelID := strings.TrimSpace(input.Alias), strings.TrimSpace(input.Model)
	if alias == "" || modelID == "" {
		return nil, errors.New("add_model needs an alias and the exact model ID from search_models")
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
		return nil, fmt.Errorf("%s does not list a model with the ID %q; search_models for it and pass the exact ID", provider, modelID)
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
	return json.Marshal(map[string]string{"result": added + " I'm restarting to load it; once I'm back, ask to switch to " + alias + "."})
}

func (t settingsTool) selectAlias(ctx context.Context, alias string) (json.RawMessage, error) {
	if alias == "" {
		return nil, errors.New("select_model needs an alias")
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

func (t settingsTool) aliasOnDisk(alias string) (config.ModelAliasConfig, bool) {
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return config.ModelAliasConfig{}, false
	}
	configured, ok := document.ModelAliases[alias]
	return configured, ok
}

// openRouterProviders is the configured OpenRouter providers that can be
// browsed, narrowed to one when the call named it.
func (t settingsTool) openRouterProviders(named string) (config.Config, []string, error) {
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

// settingsView is show's answer. Personal is this account's alone; Shared is
// the deployment's, the same for everyone; Connected is what Eggy can reach;
// ElsewhereChanged names, for each setting this tool does not change, the one
// place that does, so "can you connect my GitHub" gets a true answer.
type settingsView struct {
	Personal         personalSettings  `json:"yours"`
	Shared           sharedSettings    `json:"shared"`
	Connected        connected         `json:"connected"`
	RestartPending   bool              `json:"restart_pending,omitempty"`
	ElsewhereChanged map[string]string `json:"changed_elsewhere"`
}

type personalSettings struct {
	Model        string   `json:"model"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
	Effort       string   `json:"effort"`
	Efforts      []string `json:"efforts,omitempty"`
	Thinking     bool     `json:"thinking_shown"`
	WhenIAsk     string   `json:"when_i_ask,omitempty"`
	CheckIns     bool     `json:"check_ins"`
}

type sharedSettings struct {
	CheckInEvery       string `json:"check_in_every"`
	CheckInHours       string `json:"check_in_hours"`
	CheckInInstruction string `json:"check_in_instruction,omitempty"`
	Timezone           string `json:"timezone,omitempty"`
	ImageModel         string `json:"image_model,omitempty"`
}

type connected struct {
	Channels   []string `json:"channels"`
	MCPServers []string `json:"mcp_servers,omitempty"`
	Google     string   `json:"google,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// elsewhereChanged is where a setting outside this tool lives. The panel's
// names, so what Eggy tells someone is what they will find.
var elsewhereChanged = map[string]string{
	"soul (who you are and how you sound)": "ask you -- the memory tool rewrites SOUL.md -- or Settings → Soul",
	"watch list (what check-ins look at)":  "ask you, or Settings → Check-ins",
	"never asking (/mode auto)":            "only the person themselves: /mode auto in Telegram, or Settings → Model & when Eggy asks",
	"providers, API keys, model aliases":   "Settings → Models; you can add an OpenRouter model with add_model",
	"people and sign-in":                   "Settings → People",
	"Telegram and Discord":                 "Customize → Channels; each person links their own from Settings → People",
	"MCP servers and Google":               "Customize → Connectors",
	"skills":                               "Customize → Skills",
	"finance, agent runner, marketplace":   "Customize → Extensions",
	"tracing, raw config.yaml":             "Settings → Advanced",
	"schedules":                            "ask you (the schedule tool), or the Scheduled page",
}

func (t settingsTool) show(ctx context.Context) (json.RawMessage, error) {
	runtime := t.s.AgentRuntime
	model, err := runtime.SelectedModel(ctx)
	if err != nil {
		return nil, err
	}
	effort, err := runtime.ReasoningEffort(ctx)
	if err != nil {
		return nil, err
	}
	if effort == "" {
		effort = "default"
	}
	thinking, err := runtime.ShowThinking(ctx)
	if err != nil {
		return nil, err
	}
	checkIns, err := runtime.Heartbeat(ctx)
	if err != nil {
		return nil, err
	}
	view := settingsView{
		Personal: personalSettings{
			Model: model, DefaultModel: t.s.DefaultModel, Models: t.s.ModelAliases,
			Effort: effort, Efforts: runtime.ReasoningEfforts(model), Thinking: thinking, CheckIns: checkIns,
		},
		ElsewhereChanged: elsewhereChanged,
	}
	if t.s.Approvals != nil {
		if mode, err := t.s.Approvals.Mode(ctx); err == nil {
			view.Personal.WhenIAsk = ModeMessage(mode)
		}
	}
	// The file, not the process: a value written since the last restart is
	// what the panel shows, and restart_pending says it is not live yet.
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return nil, err
	}
	view.Shared = sharedView(document)
	view.Connected = connectedView(document)
	view.RestartPending = t.s.RestartPending != nil && t.s.RestartPending()
	return json.Marshal(view)
}

func sharedView(cfg config.Config) sharedSettings {
	shared := sharedSettings{CheckInEvery: "off", CheckInHours: "any hour", Timezone: cfg.Agent.Timezone, ImageModel: cfg.Agent.ImageModel}
	if interval := cfg.Heartbeat.Interval.Value(); interval > 0 {
		shared.CheckInEvery = interval.String()
		if !cfg.TelegramEnabled() {
			shared.CheckInEvery += " (not running: check-ins arrive on Telegram, which is off)"
		}
	}
	if hours := cfg.Heartbeat.ActiveHours; hours.Configured() {
		shared.CheckInHours = hours.Start + "-" + hours.End
	}
	shared.CheckInInstruction = cfg.Heartbeat.Instruction
	return shared
}

func connectedView(cfg config.Config) connected {
	view := connected{Channels: []string{"web"}}
	if cfg.TelegramEnabled() {
		view.Channels = append(view.Channels, "telegram")
	}
	if cfg.DiscordEnabled() {
		view.Channels = append(view.Channels, "discord")
	}
	for name, server := range cfg.MCP.Servers {
		state := "on"
		if !server.Enabled {
			state = "off"
		}
		view.MCPServers = append(view.MCPServers, name+" ("+state+")")
	}
	slices.Sort(view.MCPServers)
	if cfg.Google.Enabled {
		view.Google = strings.Join(cfg.Google.Products, ", ")
		if cfg.Google.ExpectedEmail != "" {
			view.Google += " as " + cfg.Google.ExpectedEmail + ", Eggy's own account"
		}
	}
	for name, on := range map[string]bool{"finance": cfg.Finance.Enabled, "agent runner": cfg.AgentRunner.Enabled, "marketplace": cfg.Marketplace.Enabled} {
		if on {
			view.Extensions = append(view.Extensions, name)
		}
	}
	slices.Sort(view.Extensions)
	return view
}

func (t settingsTool) setEffort(ctx context.Context, effort string) (json.RawMessage, error) {
	if effort == "" {
		return nil, errors.New("set_effort needs an effort")
	}
	if effort == "default" {
		effort = ""
	}
	if err := t.s.AgentRuntime.SelectReasoningEffort(ctx, effort); err != nil {
		model, _ := t.s.AgentRuntime.SelectedModel(ctx)
		return nil, fmt.Errorf("%w; %s offers: default, %s", err, model, strings.Join(t.s.AgentRuntime.ReasoningEfforts(model), ", "))
	}
	if effort == "" {
		effort = "the provider's default"
	}
	return result("Effort set to " + effort + ", from the next message on.")
}

// themeView is everything a turn needs to design a look: what is chosen, what
// may be chosen, and the default palettes a custom look is laid over.
type themeView struct {
	Scheme   string                 `json:"scheme"`
	Look     ports.LookView         `json:"look"`
	Defaults ports.PaletteOverrides `json:"default_palettes"`
	Note     string                 `json:"note"`
}

func (t settingsTool) showTheme(ctx context.Context) (json.RawMessage, error) {
	scheme, err := t.s.AgentRuntime.Theme(ctx)
	if err != nil {
		return nil, err
	}
	if scheme == "" {
		scheme = "deployment default"
	}
	look, err := t.s.AgentRuntime.Look(ctx)
	if err != nil {
		return nil, err
	}
	light, dark := services.DefaultPalettes()
	return json.Marshal(themeView{
		Scheme: scheme, Look: look, Defaults: ports.PaletteOverrides{Light: light, Dark: dark},
		Note: "A custom look names only the tokens it changes, per mode; the rest keep the default. Keep related tokens together: primary with accent-600, a surface with the neutral step it matches. Text pairs must stay at 4.5:1 or set_theme refuses with the pair that failed.",
	})
}

func (t settingsTool) setTheme(ctx context.Context, input settingsToolInput) (json.RawMessage, error) {
	change := ports.LookChange{Look: input.Look, TextFont: input.TextFont, CodeFont: input.CodeFont}
	if input.Light != nil || input.Dark != nil {
		change.Custom = &ports.PaletteOverrides{Light: input.Light, Dark: input.Dark}
	}
	if input.Scheme == nil && change == (ports.LookChange{}) {
		return nil, errors.New("set_theme needs scheme, look, light, dark, text_font or code_font")
	}
	// Every field is checked before anything is stored, so one refused field
	// does not leave half the request applied: the scheme here, the look by
	// SetLook, which checks all of its fields before it writes.
	if input.Scheme != nil {
		if err := services.CheckTheme(*input.Scheme); err != nil {
			return nil, err
		}
	}
	if change != (ports.LookChange{}) {
		if err := t.s.AgentRuntime.SetLook(ctx, change); err != nil {
			return nil, err
		}
	}
	if input.Scheme != nil {
		if err := t.s.AgentRuntime.SetTheme(ctx, *input.Scheme); err != nil {
			return nil, err
		}
	}
	look, err := t.s.AgentRuntime.Look(ctx)
	if err != nil {
		return nil, err
	}
	return result(fmt.Sprintf("Look set: %s, %s text, %s code. It applies the next time the panel loads; Settings → Model & when Eggy asks → Appearance undoes it.", look.Look, look.TextFont, look.CodeFont))
}

func (t settingsTool) setThinking(ctx context.Context, on *bool) (json.RawMessage, error) {
	if on == nil {
		return nil, errors.New("set_thinking needs on")
	}
	if err := t.s.AgentRuntime.SetShowThinking(ctx, *on); err != nil {
		return nil, err
	}
	if *on {
		return result("Thinking shown: the model's reasoning appears before its replies, when the provider sends it. This does not make it reason more; set_effort does.")
	}
	return result("Thinking hidden.")
}

// setMode changes how much the person is asked, but never to auto. AGENTS.md
// holds that nothing may select auto on the owner's behalf, and a tool call is
// exactly that: auto is reached only by the person's own /mode or panel choice.
func (t settingsTool) setMode(ctx context.Context, requested string) (json.RawMessage, error) {
	if t.s.Approvals == nil {
		return nil, errors.New("approvals are unavailable")
	}
	mode := ports.ApprovalMode(strings.ToLower(strings.TrimSpace(requested)))
	if mode == ports.ModeAuto {
		return nil, errors.New("only the person can turn asking off, themselves: /mode auto in Telegram, or Settings → Model & when Eggy asks")
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("%q is not a mode; use strict or normal", requested)
	}
	if err := t.s.Approvals.SetMode(ctx, mode); err != nil {
		return nil, err
	}
	return result(ModeMessage(mode))
}

func (t settingsTool) setCheckins(ctx context.Context, on *bool) (json.RawMessage, error) {
	if on == nil {
		return nil, errors.New("set_checkins needs on")
	}
	if err := t.s.AgentRuntime.SetHeartbeat(ctx, *on); err != nil {
		return nil, err
	}
	if !*on {
		return result("Check-ins off. I won't message this person unprompted.")
	}
	return result(t.s.heartbeatOnMessage())
}

// setCheckinSchedule is the panel's Check-ins form from chat. config.SetHeartbeat
// replaces the interval and active hours whole -- a blank interval means off --
// so every field the call leaves out is filled from the file first; otherwise
// "check in every hour" would also clear the hours someone set.
func (t settingsTool) setCheckinSchedule(input settingsToolInput) (json.RawMessage, error) {
	if input.Interval == nil && input.ActiveHours == nil && input.Instruction == nil {
		return nil, errors.New("set_checkin_schedule needs an interval, active_hours or instruction")
	}
	document, err := config.LoadDocument(t.s.ConfigPath)
	if err != nil {
		return nil, err
	}
	interval := ""
	if current := document.Heartbeat.Interval.Value(); current > 0 {
		interval = current.String()
	}
	if input.Interval != nil {
		interval = strings.TrimSpace(*input.Interval)
		if isOff(interval) {
			interval = ""
		} else if parsed, err := time.ParseDuration(interval); err != nil || parsed <= 0 {
			return nil, fmt.Errorf("interval %q is not a duration such as 2h or 30m", interval)
		}
	}
	start, end := document.Heartbeat.ActiveHours.Start, document.Heartbeat.ActiveHours.End
	if input.ActiveHours != nil {
		start, end = "", ""
		if hours := strings.TrimSpace(*input.ActiveHours); !isAlways(hours) {
			var found bool
			start, end, found = strings.Cut(hours, "-")
			if !found {
				return nil, fmt.Errorf("active_hours %q is not a window such as 08:00-22:00", hours)
			}
		}
	}
	instruction := ""
	if input.Instruction != nil {
		instruction = *input.Instruction
	}
	if err := config.SetHeartbeat(t.s.ConfigPath, interval, instruction, strings.TrimSpace(start), strings.TrimSpace(end)); err != nil {
		return nil, err
	}
	saved := "Saved the check-in schedule."
	if interval == "" {
		saved = "Check-ins are off for everyone."
	}
	restart, restarting := Restart(t.s.Restarter, t.s.ConfigPath, t.s.Getenv)
	if !restarting {
		return result(saved + " It applies after a restart, which did not happen: " + restart)
	}
	return result(saved + " I'm restarting to apply it.")
}

func isOff(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "0", "never":
		return true
	}
	return false
}

func isAlways(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "always", "any", "anytime", "all day":
		return true
	}
	return false
}
