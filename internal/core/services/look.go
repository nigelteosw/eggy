package services

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// A look is data, never code: colour tokens the panel already defines, and a
// font from a fixed list. Nothing here is CSS text, a selector or a URL, so a
// look the agent writes can recolour and refont the panel and do nothing else.
// The panel paints the overrides a look resolves to over its own stylesheet;
// the default look resolves to none.

const (
	LookDefault  = "eggshell"
	LookCastIron = "cast-iron"
	LookCustom   = "custom"
)

// Palette modes an account may choose. They match the deployment default's
// names in internal/config, which core may not import.
var themeModes = []string{"system", "light", "dark"}

// paletteTokens is every colour token in website/src/app/index.css. A test
// reads the stylesheet and fails if the two lists differ.
var paletteTokens = []string{
	"background", "foreground", "card", "card-foreground",
	"primary", "primary-foreground", "secondary", "secondary-foreground",
	"muted", "muted-foreground", "accent", "accent-foreground",
	"border", "input", "ring", "surface",
	"neutral-100", "neutral-200", "neutral-300", "neutral-400", "neutral-500",
	"neutral-600", "neutral-700", "neutral-800", "neutral-900",
	"accent-100", "accent-200", "accent-300", "accent-400", "accent-500",
	"accent-600", "accent-700", "accent-800", "accent-900",
	"tomato", "tomato-soft", "on-tomato", "chive", "yolk-soft", "yolk-ink",
}

// defaultLight and defaultDark are the stylesheet's own palettes, held here so
// a partial override can be checked for contrast against what it lands on. A
// test pins them to index.css value for value.
var defaultLight = map[string]string{
	"background": "40 45% 94%", "foreground": "33 15% 15%",
	"card": "40 100% 98%", "card-foreground": "33 15% 15%",
	"primary": "41 79% 50%", "primary-foreground": "33 15% 15%",
	"secondary": "40 36% 88%", "secondary-foreground": "33 15% 15%",
	"muted": "40 36% 88%", "muted-foreground": "35 11% 38%",
	"accent": "40 36% 88%", "accent-foreground": "33 15% 15%",
	"border": "39 30% 83%", "input": "40 29% 77%", "ring": "33 15% 15%",
	"surface":     "40 100% 98%",
	"neutral-100": "41 41% 89%", "neutral-200": "41 32% 85%", "neutral-300": "39 30% 83%",
	"neutral-400": "40 23% 72%", "neutral-500": "38 15% 60%", "neutral-600": "35 10% 49%",
	"neutral-700": "35 11% 38%", "neutral-800": "35 13% 26%", "neutral-900": "33 15% 15%",
	"accent-100": "44 87% 94%", "accent-200": "43 87% 88%", "accent-300": "43 85% 76%",
	"accent-400": "43 82% 65%", "accent-500": "41 79% 50%", "accent-600": "40 86% 41%",
	"accent-700": "39 100% 27%", "accent-800": "39 100% 21%", "accent-900": "39 100% 12%",
	"tomato": "10 61% 40%", "tomato-soft": "16 63% 93%", "on-tomato": "40 100% 98%",
	"chive": "107 30% 33%", "yolk-soft": "43 87% 88%", "yolk-ink": "40 100% 18%",
}

var defaultDark = map[string]string{
	"background": "220 10% 6%", "foreground": "240 4% 95%",
	"card": "220 7% 8%", "card-foreground": "240 4% 95%",
	"primary": "33 81% 54%", "primary-foreground": "220 10% 6%",
	"secondary": "220 7% 11%", "secondary-foreground": "240 4% 95%",
	"muted": "220 7% 11%", "muted-foreground": "220 5% 56%",
	"accent": "220 7% 11%", "accent-foreground": "240 4% 95%",
	"border": "220 9% 13%", "input": "220 7% 22%", "ring": "240 4% 95%",
	"surface":     "220 7% 8%",
	"neutral-100": "220 7% 8%", "neutral-200": "220 7% 11%", "neutral-300": "220 9% 13%",
	"neutral-400": "220 7% 22%", "neutral-500": "218 6% 39%", "neutral-600": "220 5% 48%",
	"neutral-700": "220 5% 56%", "neutral-800": "220 4% 78%", "neutral-900": "240 4% 95%",
	"accent-100": "33 45% 10%", "accent-200": "33 53% 14%", "accent-300": "33 58% 21%",
	"accent-400": "33 63% 28%", "accent-500": "33 73% 38%", "accent-600": "33 81% 54%",
	"accent-700": "33 90% 66%", "accent-800": "33 89% 76%", "accent-900": "33 91% 87%",
	"tomato": "11 67% 63%", "tomato-soft": "12 30% 14%", "on-tomato": "220 10% 6%",
	"chive": "105 28% 61%", "yolk-soft": "33 45% 12%", "yolk-ink": "33 90% 66%",
}

// builtInLooks are the looks that ship. Cast iron is the warm charcoal dark
// mode Eggy had before it took Herdr's cooler palette; its light mode is the
// default Eggshell.
var builtInLooks = map[string]ports.PaletteOverrides{
	LookDefault: {},
	LookCastIron: {Dark: map[string]string{
		"background": "0 0% 8%", "foreground": "0 0% 93%",
		"card": "0 0% 12%", "card-foreground": "0 0% 93%",
		"primary": "41 88% 57%", "primary-foreground": "0 0% 9%",
		"secondary": "0 0% 16%", "secondary-foreground": "0 0% 93%",
		"muted": "0 0% 16%", "muted-foreground": "0 0% 64%",
		"accent": "0 0% 16%", "accent-foreground": "0 0% 93%",
		"border": "0 0% 21%", "input": "0 0% 29%", "ring": "0 0% 93%",
		"surface":     "0 0% 12%",
		"neutral-100": "0 0% 12%", "neutral-200": "0 0% 16%", "neutral-300": "0 0% 21%",
		"neutral-400": "0 0% 29%", "neutral-500": "0 0% 42%", "neutral-600": "0 0% 54%",
		"neutral-700": "0 0% 64%", "neutral-800": "0 0% 81%", "neutral-900": "0 0% 93%",
		"accent-100": "39 45% 11%", "accent-200": "42 53% 15%", "accent-300": "41 58% 22%",
		"accent-400": "41 63% 29%", "accent-500": "40 73% 38%", "accent-600": "41 75% 51%",
		"accent-700": "43 90% 66%", "accent-800": "43 89% 76%", "accent-900": "43 91% 87%",
		"tomato": "11 67% 63%", "tomato-soft": "14 35% 17%", "on-tomato": "0 0% 9%",
		"chive": "105 28% 61%", "yolk-soft": "52 45% 13%", "yolk-ink": "43 90% 66%",
	}},
}

// The faces a look may use, all served by Google Fonts. The first of each is
// the default. A name outside these is refused, so the panel only ever builds
// a font request for a family named here.
var (
	textFonts = []string{"Geist", "Inter", "IBM Plex Sans", "Atkinson Hyperlegible", "Fraunces"}
	codeFonts = []string{"JetBrains Mono", "IBM Plex Mono", "Fira Code", "Space Mono"}
)

// textPairs are text tokens on the surfaces they are drawn on. Every pair
// must reach WCAG AA (4.5:1), in the stylesheet and in any look.
var textPairs = [][2]string{
	{"foreground", "background"}, {"foreground", "card"},
	{"muted-foreground", "background"}, {"muted-foreground", "card"}, {"muted-foreground", "muted"},
	{"accent-700", "background"}, {"primary-foreground", "primary"},
	{"tomato", "background"}, {"tomato", "card"}, {"tomato", "tomato-soft"}, {"on-tomato", "tomato"},
	{"chive", "background"}, {"chive", "card"},
	{"yolk-ink", "yolk-soft"}, {"yolk-ink", "background"},
}

var hslPattern = regexp.MustCompile(`^(\d{1,3}(?:\.\d+)?) (\d{1,3}(?:\.\d+)?)% (\d{1,3}(?:\.\d+)?)%$`)

// DefaultPalettes returns copies of the stylesheet's own light and dark
// palettes, which a custom look's overrides are laid over.
func DefaultPalettes() (light, dark map[string]string) {
	return maps.Clone(defaultLight), maps.Clone(defaultDark)
}

// lookNames lists the looks in the order the panel offers them.
func lookNames() []string { return []string{LookDefault, LookCastIron, LookCustom} }

// validatePalette refuses an override that names a token the stylesheet does
// not define, a value that is not an HSL triple, or a result that leaves any
// text pair below 4.5:1 once laid over base.
func validatePalette(mode string, overrides, base map[string]string) error {
	merged := make(map[string][3]float64, len(base))
	for name, value := range base {
		merged[name], _ = parseHSL(value)
	}
	for name, value := range overrides {
		if !slices.Contains(paletteTokens, name) {
			return fmt.Errorf("%s: %q is not a colour token; the tokens are %s", mode, name, strings.Join(paletteTokens, ", "))
		}
		parsed, err := parseHSL(value)
		if err != nil {
			return fmt.Errorf("%s %s: %w", mode, name, err)
		}
		merged[name] = parsed
	}
	for _, pair := range textPairs {
		if ratio := contrastRatio(merged[pair[0]], merged[pair[1]]); ratio < 4.5 {
			return fmt.Errorf("%s: %s on %s is %.1f:1, below the 4.5:1 text needs to stay readable", mode, pair[0], pair[1], ratio)
		}
	}
	return nil
}

func parseHSL(value string) ([3]float64, error) {
	match := hslPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return [3]float64{}, fmt.Errorf("%q must be \"H S%% L%%\", such as \"33 81%% 54%%\"", value)
	}
	var out [3]float64
	for i := range out {
		out[i], _ = strconv.ParseFloat(match[i+1], 64)
	}
	if out[0] > 360 || out[1] > 100 || out[2] > 100 {
		return [3]float64{}, fmt.Errorf("%q is out of range: hue 0-360, saturation and lightness 0-100%%", value)
	}
	return out, nil
}

func contrastRatio(a, b [3]float64) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hsl [3]float64) float64 {
	s, l := hsl[1]/100, hsl[2]/100
	chroma := (1 - math.Abs(2*l-1)) * s
	h := math.Mod(hsl[0], 360) / 60
	x := chroma * (1 - math.Abs(math.Mod(h, 2)-1))
	var r, g, b float64
	switch {
	case h < 1:
		r, g = chroma, x
	case h < 2:
		r, g = x, chroma
	case h < 3:
		g, b = chroma, x
	case h < 4:
		g, b = x, chroma
	case h < 5:
		r, b = x, chroma
	default:
		r, b = chroma, x
	}
	m := l - chroma/2
	linear := func(c float64) float64 {
		c += m
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// Look reports the acting account's look as the panel paints it.
func (r *AgentRuntime) Look(ctx context.Context) (ports.LookView, error) {
	state, err := r.store.Load(ctx)
	if err != nil {
		return ports.LookView{}, err
	}
	agent := state.Agent
	view := ports.LookView{
		Look: LookDefault, TextFont: textFonts[0], CodeFont: codeFonts[0],
		HasCustom: agent.CustomLook != nil,
		Looks:     lookNames(), TextFonts: textFonts, CodeFonts: codeFonts,
	}
	if agent.TextFont != "" {
		view.TextFont = agent.TextFont
	}
	if agent.CodeFont != "" {
		view.CodeFont = agent.CodeFont
	}
	switch {
	case agent.Look == LookCustom && agent.CustomLook != nil:
		view.Look, view.Overrides = LookCustom, *agent.CustomLook
	case agent.Look != "" && agent.Look != LookCustom:
		if overrides, ok := builtInLooks[agent.Look]; ok {
			view.Look, view.Overrides = agent.Look, overrides
		}
	}
	return view, nil
}

// SetLook is the one write path for a look, behind the panel and the settings
// tool alike. Everything is checked before anything is stored, so a refused
// change leaves the account's look as it was.
func (r *AgentRuntime) SetLook(ctx context.Context, change ports.LookChange) error {
	if change.Custom != nil {
		if len(change.Custom.Light) == 0 && len(change.Custom.Dark) == 0 {
			return errors.New("a custom look needs colours for light, dark or both")
		}
		if err := validatePalette("light", change.Custom.Light, defaultLight); err != nil {
			return err
		}
		if err := validatePalette("dark", change.Custom.Dark, defaultDark); err != nil {
			return err
		}
	}
	if change.Look != nil {
		name := strings.TrimSpace(*change.Look)
		if name != "" && !slices.Contains(lookNames(), name) {
			return fmt.Errorf("look must be one of %s", strings.Join(lookNames(), ", "))
		}
	}
	if err := checkFont(change.TextFont, textFonts, "text font"); err != nil {
		return err
	}
	if err := checkFont(change.CodeFont, codeFonts, "code font"); err != nil {
		return err
	}
	if change.Look != nil && strings.TrimSpace(*change.Look) == LookCustom && change.Custom == nil {
		state, err := r.store.Load(ctx)
		if err != nil {
			return err
		}
		if state.Agent.CustomLook == nil {
			return errors.New("there is no custom look yet; describe one to set it")
		}
	}
	return r.update(ctx, func(state *ports.State) {
		agent := &state.Agent
		if change.Custom != nil {
			custom := *change.Custom
			agent.CustomLook, agent.Look = &custom, LookCustom
		}
		if change.Look != nil {
			name := strings.TrimSpace(*change.Look)
			if name == LookDefault {
				name = ""
			}
			agent.Look = name
		}
		if change.TextFont != nil {
			agent.TextFont = defaultFont(*change.TextFont, textFonts)
		}
		if change.CodeFont != nil {
			agent.CodeFont = defaultFont(*change.CodeFont, codeFonts)
		}
	})
}

func checkFont(name *string, offered []string, label string) error {
	if name == nil || strings.TrimSpace(*name) == "" || slices.Contains(offered, strings.TrimSpace(*name)) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s", label, strings.Join(offered, ", "))
}

// defaultFont stores the default face as empty, so a later change of default
// reaches an account that never chose.
func defaultFont(name string, offered []string) string {
	name = strings.TrimSpace(name)
	if name == offered[0] {
		return ""
	}
	return name
}
