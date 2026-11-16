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
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/internal/themes"
)

// A look is data, never code: colour tokens the panel already defines, and a
// font from a fixed list. Nothing here is CSS text, a selector or a URL, so a
// look the agent writes can recolour and refont the panel and do nothing else.
// The panel paints the overrides a look resolves to over its own stylesheet;
// the default look resolves to none.

// LookCustom is the account's own look; every other name is a theme in
// internal/themes/themes.json, whose first entry is the default.
const LookCustom = "custom"

// Palette modes an account may choose. They match the deployment default's
// names in internal/config, which core may not import.
var themeModes = []string{"system", "light", "dark"}

// The palettes come from internal/themes, the one file colours are written
// in. The default theme's tokens are every token a look may set, and its
// palettes are what a partial override is laid over and checked against.
var (
	allThemes     = themes.All()
	LookDefault   = allThemes[0].Name
	defaultLight  = allThemes[0].Light
	defaultDark   = allThemes[0].Dark
	paletteTokens = slices.Sorted(maps.Keys(defaultLight))
	builtInLooks  = func() map[string]ports.PaletteOverrides {
		looks := make(map[string]ports.PaletteOverrides, len(allThemes))
		for i, theme := range allThemes {
			if i == 0 {
				looks[theme.Name] = ports.PaletteOverrides{}
				continue
			}
			looks[theme.Name] = ports.PaletteOverrides{Light: theme.Light, Dark: theme.Dark}
		}
		return looks
	}()
)

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

// oklchPattern is "L% C H": lightness as a percentage, chroma, hue in degrees.
// Nothing else -- no units, no alpha, no CSS -- so a value can only ever be a
// colour inside the stylesheet's own oklch(var(--token)).
var oklchPattern = regexp.MustCompile(`^(\d{1,3}(?:\.\d+)?)% (\d(?:\.\d+)?) (\d{1,3}(?:\.\d+)?)$`)

// hslPattern is the "H S% L%" triple palettes were written in before OKLCH. It
// is read only to convert a custom look stored then; nothing new is accepted
// in it.
var hslPattern = regexp.MustCompile(`^(\d{1,3}(?:\.\d+)?) (\d{1,3}(?:\.\d+)?)% (\d{1,3}(?:\.\d+)?)%$`)

// DefaultPalettes returns copies of the stylesheet's own light and dark
// palettes, which a custom look's overrides are laid over.
func DefaultPalettes() (light, dark map[string]string) {
	return maps.Clone(defaultLight), maps.Clone(defaultDark)
}

// lookEntry is one look an account can choose: a built-in theme from
// themes.json, or a theme someone saved for everyone, which lives in eggy.db
// and so outlives a redeploy.
type lookEntry struct {
	name, label, description string
	overrides                ports.PaletteOverrides
	user                     bool
	createdBy                string
}

// UseThemes gives the runtime the store user themes are saved in. Without one
// only the built-in themes are offered and saving a theme is refused.
func (r *AgentRuntime) UseThemes(store ports.ThemeStore) { r.themes = store }

// catalog lists every look in the order the panel offers them: built-in
// themes, then saved ones, oldest first.
func (r *AgentRuntime) catalog(ctx context.Context) ([]lookEntry, error) {
	entries := make([]lookEntry, 0, len(allThemes))
	for _, theme := range allThemes {
		entries = append(entries, lookEntry{name: theme.Name, label: theme.Label, description: theme.Description, overrides: builtInLooks[theme.Name]})
	}
	if r.themes == nil {
		return entries, nil
	}
	saved, err := r.themes.ListThemes(ctx)
	if err != nil {
		return nil, err
	}
	for _, theme := range saved {
		// A built-in added later under a saved theme's name wins; the saved
		// one stays stored but is not offered twice.
		if _, builtIn := builtInLooks[theme.Name]; builtIn {
			continue
		}
		entries = append(entries, lookEntry{
			name: theme.Name, label: theme.Label, description: theme.Description,
			overrides: ports.PaletteOverrides{Light: theme.Light, Dark: theme.Dark},
			user:      true, createdBy: theme.CreatedBy,
		})
	}
	return entries, nil
}

// lookNames lists the names an account may choose, custom last.
func lookNames(entries []lookEntry) []string {
	names := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	return append(names, LookCustom)
}

// lookOptions describes each look the account can pick, in order, with the
// colours a swatch draws: the look's light and dark backgrounds and its
// accent. Custom is offered only once the account has one.
func lookOptions(entries []lookEntry, custom *ports.PaletteOverrides, account string) []ports.LookOption {
	pick := func(overrides map[string]string, base map[string]string, token string) string {
		if value, ok := overrides[token]; ok {
			return value
		}
		return base[token]
	}
	option := func(name, label, description string, look ports.PaletteOverrides) ports.LookOption {
		return ports.LookOption{
			Name: name, Label: label, Description: description,
			Light:  pick(look.Light, defaultLight, "background"),
			Dark:   pick(look.Dark, defaultDark, "background"),
			Accent: pick(look.Light, defaultLight, "primary"),
		}
	}
	options := make([]ports.LookOption, 0, len(entries)+1)
	for _, entry := range entries {
		item := option(entry.name, entry.label, entry.description, entry.overrides)
		item.User, item.Mine = entry.user, entry.user && entry.createdBy == account
		options = append(options, item)
	}
	if custom != nil {
		options = append(options, option(LookCustom, "Custom", "The theme you asked Eggy for.", legacyLook(*custom)))
	}
	return options
}

// validatePalette refuses an override that names a token the stylesheet does
// not define, a value that is not an OKLCH triple, or a result that leaves any
// text pair below 4.5:1 once laid over base.
func validatePalette(mode string, overrides, base map[string]string) error {
	merged := make(map[string][3]float64, len(base))
	for name, value := range base {
		merged[name], _ = parseOKLCH(value)
	}
	for name, value := range overrides {
		if !slices.Contains(paletteTokens, name) {
			return fmt.Errorf("%s: %q is not a colour token; the tokens are %s", mode, name, strings.Join(paletteTokens, ", "))
		}
		parsed, err := parseOKLCH(value)
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

func parseOKLCH(value string) ([3]float64, error) {
	match := oklchPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return [3]float64{}, fmt.Errorf("%q must be OKLCH \"L%% C H\", such as \"73.6%% 0.151 65.6\"", value)
	}
	var out [3]float64
	for i := range out {
		out[i], _ = strconv.ParseFloat(match[i+1], 64)
	}
	if out[0] > 100 || out[1] > 0.4 || out[2] > 360 {
		return [3]float64{}, fmt.Errorf("%q is out of range: lightness 0-100%%, chroma 0-0.4, hue 0-360", value)
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

// linearSRGB converts an OKLCH triple to linear sRGB, clipped to the gamut a
// screen shows, which is what the browser paints for a colour outside it.
func linearSRGB(oklch [3]float64) (r, g, b float64) {
	l, c, h := oklch[0]/100, oklch[1], oklch[2]*math.Pi/180
	a, bb := c*math.Cos(h), c*math.Sin(h)
	l1 := math.Pow(l+0.3963377774*a+0.2158037573*bb, 3)
	m1 := math.Pow(l-0.1055613458*a-0.0638541728*bb, 3)
	s1 := math.Pow(l-0.0894841775*a-1.2914855480*bb, 3)
	clip := func(v float64) float64 { return math.Min(1, math.Max(0, v)) }
	return clip(4.0767416621*l1 - 3.3077115913*m1 + 0.2309699292*s1),
		clip(-1.2684380046*l1 + 2.6097574011*m1 - 0.3413193965*s1),
		clip(-0.0041960863*l1 - 0.7034186147*m1 + 1.7076147010*s1)
}

func luminance(oklch [3]float64) float64 {
	r, g, b := linearSRGB(oklch)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// legacyToOKLCH rewrites a value stored as "H S% L%" before palettes moved to
// OKLCH, so a custom look described then still paints. Anything else is
// returned as it is.
func legacyToOKLCH(value string) string {
	match := hslPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return value
	}
	var hsl [3]float64
	for i := range hsl {
		hsl[i], _ = strconv.ParseFloat(match[i+1], 64)
	}
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
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	r, g, b = linear(r), linear(g), linear(b)
	l1 := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m1 := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s1 := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	lightness := 0.2104542553*l1 + 0.7936177850*m1 - 0.0040720468*s1
	a := 1.9779984951*l1 - 2.4285922050*m1 + 0.4505937099*s1
	bb := 0.0259040371*l1 + 0.7827717662*m1 - 0.8086757660*s1
	c := math.Hypot(a, bb)
	hue := math.Atan2(bb, a) * 180 / math.Pi
	if hue < 0 {
		hue += 360
	}
	if c < 0.0005 {
		c, hue = 0, 0
	}
	round := func(v, scale float64) string { return strconv.FormatFloat(math.Round(v*scale)/scale, 'f', -1, 64) }
	return round(lightness*100, 10) + "% " + round(c, 1000) + " " + round(hue, 10)
}

// legacyLook converts every value of a stored custom look, without touching
// the stored copy.
func legacyLook(look ports.PaletteOverrides) ports.PaletteOverrides {
	convert := func(mode map[string]string) map[string]string {
		if mode == nil {
			return nil
		}
		out := make(map[string]string, len(mode))
		for name, value := range mode {
			out[name] = legacyToOKLCH(value)
		}
		return out
	}
	return ports.PaletteOverrides{Light: convert(look.Light), Dark: convert(look.Dark)}
}

// Look reports the acting account's look as the panel paints it.
func (r *AgentRuntime) Look(ctx context.Context) (ports.LookView, error) {
	state, err := r.store.Load(ctx)
	if err != nil {
		return ports.LookView{}, err
	}
	entries, err := r.catalog(ctx)
	if err != nil {
		return ports.LookView{}, err
	}
	principal, _ := ports.PrincipalFromContext(ctx)
	agent := state.Agent
	view := ports.LookView{
		Look: LookDefault, TextFont: textFonts[0], CodeFont: codeFonts[0],
		HasCustom: agent.CustomLook != nil,
		Looks:     lookNames(entries), Options: lookOptions(entries, agent.CustomLook, principal.AccountID),
		TextFonts: textFonts, CodeFonts: codeFonts,
	}
	if agent.TextFont != "" {
		view.TextFont = agent.TextFont
	}
	if agent.CodeFont != "" {
		view.CodeFont = agent.CodeFont
	}
	switch {
	case agent.Look == LookCustom && agent.CustomLook != nil:
		view.Look, view.Overrides = LookCustom, legacyLook(*agent.CustomLook)
	case agent.Look != "" && agent.Look != LookCustom:
		// A saved theme someone has since deleted is no longer in the
		// catalog, so its last users fall back to the default.
		for _, entry := range entries {
			if entry.name == agent.Look {
				view.Look, view.Overrides = entry.name, entry.overrides
			}
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
		entries, err := r.catalog(ctx)
		if err != nil {
			return err
		}
		if names := lookNames(entries); name != "" && !slices.Contains(names, name) {
			return fmt.Errorf("look must be one of %s", strings.Join(names, ", "))
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

// themeNamePattern is a saved theme's name: lowercase words joined by
// hyphens, derived from its label.
var themeNamePattern = regexp.MustCompile(`[^a-z0-9]+`)

// SaveTheme saves the acting account's custom look as a theme everyone on the
// deployment can choose, under label, and selects it for the account. It is
// stored in eggy.db, so it outlives a redeploy. The palettes were checked when
// the custom look was set and are checked again here, so a theme can never be
// less readable than a custom look is allowed to be.
func (r *AgentRuntime) SaveTheme(ctx context.Context, label, description string) (string, error) {
	if r.themes == nil {
		return "", errors.New("saving themes is unavailable")
	}
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", err
	}
	label, description = strings.TrimSpace(label), strings.TrimSpace(description)
	if label == "" || len([]rune(label)) > 40 {
		return "", errors.New("a theme needs a name of up to 40 characters")
	}
	if len([]rune(description)) > 120 {
		return "", errors.New("a theme's description is up to 120 characters")
	}
	name := strings.Trim(themeNamePattern.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if name == "" {
		return "", errors.New("a theme's name needs a letter or a digit")
	}
	if name == LookCustom {
		return "", ports.ErrThemeExists
	}
	entries, err := r.catalog(ctx)
	if err != nil {
		return "", err
	}
	if slices.Contains(lookNames(entries), name) {
		return "", ports.ErrThemeExists
	}
	state, err := r.store.Load(ctx)
	if err != nil {
		return "", err
	}
	if state.Agent.CustomLook == nil {
		return "", errors.New("there is no custom theme to save yet; ask Eggy for one first")
	}
	look := legacyLook(*state.Agent.CustomLook)
	if err := validatePalette("light", look.Light, defaultLight); err != nil {
		return "", err
	}
	if err := validatePalette("dark", look.Dark, defaultDark); err != nil {
		return "", err
	}
	if description == "" {
		description = "Saved by someone on this Eggy."
	}
	theme := ports.UserTheme{
		Name: name, Label: label, Description: description,
		Light: look.Light, Dark: look.Dark,
		CreatedBy: principal.AccountID, CreatedAt: time.Now(),
	}
	if err := r.themes.CreateTheme(ctx, theme); err != nil {
		return "", err
	}
	return name, r.update(ctx, func(state *ports.State) { state.Agent.Look = name })
}

// DeleteTheme removes a saved theme. Only the account that saved it may: the
// one authorization question is whether this account owns the record.
func (r *AgentRuntime) DeleteTheme(ctx context.Context, name string) error {
	if r.themes == nil {
		return ports.ErrThemeNotFound
	}
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return err
	}
	entries, err := r.catalog(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.name != name {
			continue
		}
		if !entry.user {
			return errors.New("a built-in theme cannot be deleted")
		}
		if entry.createdBy != principal.AccountID {
			return errors.New("only the person who saved a theme can delete it")
		}
		return r.themes.DeleteTheme(ctx, name)
	}
	return ports.ErrThemeNotFound
}
