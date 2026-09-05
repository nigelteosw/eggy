// The settings panel: one read and one write per config section, plus the raw
// editor that is the repair path when a section form cannot express the fix.
// Every write lands in internal/config under the same lock and validation the
// chat surface uses -- these are a view onto that authority, never a second one.
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
)

// rawConfigGetRoute hands back config.yaml as the owner wrote it, comments and
// all. Shared by safe mode and the running panel: safe mode is where it began,
// but reaching it only by breaking Eggy first meant the file was uneditable
// from a browser in every case except the emergency.
func rawConfigGetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		body, err := config.ReadConfigText(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
}

// rawConfigSetRoute replaces config.yaml, but only with a body LoadConfig has
// already accepted, so neither surface can write a config that would not
// start. repaired may be nil: safe mode uses it to hand control back to the
// supervisor, while the running daemon has nothing to retry -- a live process
// picks the new file up on its next restart, which is the owner's call and
// which /restart in chat performs.
func rawConfigSetRoute(configPath string, getenv func(string) string, repaired func()) http.HandlerFunc {
	if getenv == nil {
		getenv = os.Getenv
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// The file is small by construction and the writer is the
		// authenticated owner, but the cap keeps a stuck or hostile client
		// from filling the volume.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeWebError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		if err := config.ReplaceConfig(configPath, body, getenv); err != nil {
			// A rejected config is never written: the owner still has the file
			// they had, plus the reason this one would not have started.
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if repaired != nil {
			repaired()
			writeWebResult(w, webResult{State: webSuccess, Title: "Config saved.", Detail: "Eggy is starting up again. Reload in a few seconds."})
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Config saved.", Detail: restartToApply})
	}
}

// configuredTheme reads the theme for the probe above. A config it cannot
// read falls back to the default rather than failing the probe: the mode is
// the load-bearing half of that response, and refusing to report it because a
// cosmetic preference was unreadable would blank the whole panel.
func configuredTheme(configPath string) func() string {
	return func() string {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			return config.ThemeDark
		}
		return cfg.Appearance.ResolvedTheme()
	}
}

func webConfigGetRoute(configPath, section string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result := webResult{State: webSuccess}
		switch section {
		case "providers":
			names := slices.Sorted(maps.Keys(cfg.Providers))
			result.TableHeaders = []string{"Provider", "Adapter", "Base URL", "API key env", "Discover models"}
			for _, name := range names {
				provider := cfg.Providers[name]
				discover := "no"
				if provider.DiscoversModels() {
					discover = "yes"
				}
				result.TableRows = append(result.TableRows, []string{name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv, discover})
			}
		case "models":
			aliases := slices.Sorted(maps.Keys(cfg.ModelAliases))
			result.TableHeaders = []string{"Alias", "Provider", "Model", "Reasoning efforts"}
			for _, alias := range aliases {
				model := cfg.ModelAliases[alias]
				result.TableRows = append(result.TableRows, []string{alias, model.Provider, model.Model, strings.Join(model.ReasoningEfforts, ", ")})
			}
			// Which providers can be browsed rides along with the section the
			// card already fetches, rather than costing a second round trip to
			// answer a question that only changes when config does.
			if webConfig.ModelDiscovery != nil {
				result.Lines = webConfig.ModelDiscovery.DiscoverableProviders()
			}
		case "google":
			// One row, because there is one grant. A second row would suggest
			// per-product configuration that does not exist.
			state := "disabled"
			if cfg.Google.Enabled {
				state = "enabled"
			}
			result.TableHeaders = []string{"State", "Client ID", "Client secret env", "Products"}
			result.TableRows = append(result.TableRows, []string{state, cfg.Google.ClientID, cfg.Google.ClientSecretEnv, strings.Join(cfg.Google.Products, ", ")})
			result.Fields = googleApprovalFields(cfg, webConfig.GoogleActions)
		case "heartbeat":
			// One row, like Google: there is one heartbeat. A zero interval
			// is reported as "off" rather than as "0s", because off is the
			// state the owner is looking for and a duration that means off
			// reads like a misconfiguration.
			interval := "off"
			if cfg.Heartbeat.Interval.Value() > 0 {
				interval = cfg.Heartbeat.Interval.Value().String()
			}
			instruction := cfg.Heartbeat.Instruction
			if strings.TrimSpace(instruction) == "" {
				instruction = "(built-in default)"
			}
			// The window and the history relaxation are reported even though
			// only the window is settable here: a setting the panel hides is
			// one the owner cannot discover is on.
			window := "any hour"
			if hours := cfg.Heartbeat.ActiveHours; hours.Configured() {
				window = hours.Start + "-" + hours.End
			}
			history := "isolated"
			if cfg.Heartbeat.IncludeRecentHistory {
				history = "recent history (config.yaml only)"
			}
			result.TableHeaders = []string{"Interval", "Instruction", "Active hours", "Context"}
			result.TableRows = append(result.TableRows, []string{interval, instruction, window, history})
		case "tracing":
			// One row: there is one trace recorder. Off is reported as off
			// rather than as a set of limits that never apply, because that
			// is the state the owner is looking for.
			state := "on"
			if !cfg.Tracing.Active() {
				state = "off"
			}
			result.TableHeaders = []string{"Tracing", "Turns kept", "Kept for", "Max body"}
			result.TableRows = append(result.TableRows, []string{
				state,
				strconv.Itoa(cfg.Tracing.KeepTurns),
				cfg.Tracing.Retention.Value().String(),
				strconv.FormatInt(cfg.Tracing.MaxBodyBytes, 10),
			})
		case "appearance":
			result.Fields = []webField{{Label: "theme", Value: cfg.Appearance.ResolvedTheme()}}
		}
		writeWebResult(w, result)
	}
}

// GoogleProductActions is one product's surface as the panel needs it: every
// action it accepts, and the subset that writes. The second is what the form
// pre-selects, so an owner opening the card sees the default they already have
// rather than an empty grid that would gate nothing if they saved it.
type GoogleProductActions struct {
	Actions   []string
	Mutations []string
}

// googleApprovalFields describes the gate to the form.
//
// require_approval_mode distinguishes the two states that look alike and are
// not: "default" means no key is stored and each tool's own classification
// decides -- including actions added by a later version -- while "custom"
// means the stored list is the whole of it, and an empty custom list gates
// nothing at all.
func googleApprovalFields(cfg config.Config, catalog map[string]GoogleProductActions) []webField {
	mode, stored := "default", []string(nil)
	if cfg.Google.RequireApproval != nil {
		mode, stored = "custom", *cfg.Google.RequireApproval
	}
	fields := []webField{
		{Label: "require_approval_mode", Value: mode},
		{Label: "require_approval", Value: strings.Join(stored, ",")},
	}
	for _, product := range slices.Sorted(maps.Keys(catalog)) {
		fields = append(fields,
			webField{Label: "actions." + product, Value: strings.Join(catalog[product].Actions, ",")},
			webField{Label: "mutations." + product, Value: strings.Join(catalog[product].Mutations, ",")},
		)
	}
	return fields
}

// checkGoogleApprovals refuses an entry the adapter would refuse at startup,
// while the existing config is still in place. The panel builds its checkboxes
// from the same catalog, so this is unreachable through the form itself -- it
// is here for anything else that can POST.
func checkGoogleApprovals(entries []string, catalog map[string]GoogleProductActions) error {
	for _, entry := range entries {
		product, action, qualified := strings.Cut(entry, ".")
		known, exists := catalog[product]
		if !exists {
			return fmt.Errorf("%q names no Google product", entry)
		}
		if !qualified {
			return fmt.Errorf("%q must name an action, as in %q, or %q for all of them", entry, product+".<action>", product+".*")
		}
		if action != "*" && !slices.Contains(known.Actions, action) {
			return fmt.Errorf("%q: %s has no action %q", entry, product, action)
		}
	}
	return nil
}

func webConfigSetRoute(configPath, section string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var named map[string]string
		if err := json.NewDecoder(r.Body).Decode(&named); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		var err error
		var title string
		switch section {
		case "providers":
			input := config.ProviderInput{
				Name: named["name"], Adapter: named["adapter"],
				BaseURL: named["base_url"], APIKeyEnv: named["api_key_env"],
			}
			// Absent leaves the key out and takes the default; only a form that
			// actually carried the control writes a choice down.
			if sent, ok := named["discover_models"]; ok {
				discover := sent != "false"
				input.DiscoverModels = &discover
			}
			err = config.SetProvider(configPath, input)
			title = "Set provider " + named["name"] + "."
		case "models":
			err = config.SetModelAlias(configPath, named["alias"], named["provider"], named["model"], named["reasoning_efforts"])
			title = "Set model " + named["alias"] + "."
		case "google":
			// Decoded by internal/config, not mapped field by field here: the
			// chat surface decodes the same keys through the same function, so
			// neither surface can grow a field the other does not know about.
			input, decodeErr := config.Values(named).GoogleInput()
			if decodeErr != nil {
				writeWebError(w, http.StatusBadRequest, decodeErr.Error())
				return
			}
			// Three states through one pointer, the same distinction the
			// setting itself turns on: absent leaves the stored list alone, a
			// pointer to nil restores the default by removing the key, and a
			// pointer to a list -- empty included -- replaces it.
			if mode, sent := named["require_approval_mode"]; sent {
				entries := config.SplitCommaList(named["require_approval"])
				if mode == "default" {
					entries = nil
				} else if entries == nil {
					entries = []string{}
				}
				if err := checkGoogleApprovals(entries, webConfig.GoogleActions); err != nil {
					writeWebError(w, http.StatusBadRequest, err.Error())
					return
				}
				input.RequireApproval = &entries
			}
			err = config.SetGoogle(configPath, input)
			title = "Saved Google Workspace."
		case "heartbeat":
			err = config.SetHeartbeat(configPath, named["interval"], named["instruction"], named["active_start"], named["active_end"])
			title = "Saved heartbeat."
			if strings.TrimSpace(named["interval"]) == "" {
				title = "Heartbeat turned off."
			}
		case "tracing":
			err = config.SetTracing(configPath, named["enabled"], named["keep_turns"], named["retention"], named["max_body_bytes"])
			title = "Saved tracing."
			if named["enabled"] == "false" {
				title = "Tracing turned off."
			}
		case "appearance":
			err = config.SetAppearance(configPath, named["theme"])
			title = "Saved appearance."
		}
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Appearance is the one section a restart does not gate: nothing in the
		// running process reads it, so the page the owner is looking at has
		// already applied it by the time this response lands.
		detail := restartToApply
		if section == "appearance" {
			detail = ""
		}
		writeWebResult(w, webResult{State: webSuccess, Title: title, Detail: detail})
	}
}
