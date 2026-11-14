package services

import (
	"context"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// stylesheetPalettes reads the panel's own palettes, so the Go copies used for
// contrast checks cannot drift from what the browser paints.
func stylesheetPalettes(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	css, err := os.ReadFile("../../../website/src/app/index.css")
	if err != nil {
		t.Fatal(err)
	}
	block := func(selector string) map[string]string {
		match := regexp.MustCompile(regexp.QuoteMeta(selector) + `\s*\{([^}]*)\}`).FindSubmatch(css)
		if match == nil {
			t.Fatalf("no %s block in index.css", selector)
		}
		tokens := map[string]string{}
		for _, m := range regexp.MustCompile(`--([\w-]+):\s*([^;]+);`).FindAllStringSubmatch(string(match[1]), -1) {
			if strings.HasPrefix(m[1], "font-") {
				continue
			}
			tokens[m[1]] = strings.TrimSpace(m[2])
		}
		return tokens
	}
	return block(":root"), block(".dark")
}

func TestLookPalettesMatchTheStylesheet(t *testing.T) {
	light, dark := stylesheetPalettes(t)
	if !maps.Equal(light, defaultLight) {
		t.Fatalf("defaultLight drifted from index.css :root\ncss=%v\ngo=%v", light, defaultLight)
	}
	if !maps.Equal(dark, defaultDark) {
		t.Fatalf("defaultDark drifted from index.css .dark\ncss=%v\ngo=%v", dark, defaultDark)
	}
	names := slices.Sorted(maps.Keys(light))
	if want := slices.Sorted(slices.Values(paletteTokens)); !slices.Equal(names, want) {
		t.Fatalf("paletteTokens drifted from index.css\ncss=%v\ngo=%v", names, want)
	}
}

// Every shipped palette keeps every text pair at WCAG AA, so a built-in can
// never be the unreadable look a custom one is refused for being.
func TestEveryBuiltInLookIsReadable(t *testing.T) {
	if err := validatePalette("light", nil, defaultLight); err != nil {
		t.Fatal(err)
	}
	if err := validatePalette("dark", nil, defaultDark); err != nil {
		t.Fatal(err)
	}
	for name, look := range builtInLooks {
		if err := validatePalette("light", look.Light, defaultLight); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := validatePalette("dark", look.Dark, defaultDark); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func lookRuntime(t *testing.T) (*AgentRuntime, context.Context) {
	t.Helper()
	runtime := NewAgentRuntime(newStateStore(t), "fast", []string{"fast"}, nil)
	return runtime, ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "42"})
}

func ptr(value string) *string { return &value }

func TestSetLookStoresAndSelectsACustomLook(t *testing.T) {
	runtime, ctx := lookRuntime(t)
	custom := ports.PaletteOverrides{Dark: map[string]string{"primary": "150 60% 45%", "accent-600": "150 60% 45%"}}
	if err := runtime.SetLook(ctx, ports.LookChange{Custom: &custom, TextFont: ptr("Inter")}); err != nil {
		t.Fatal(err)
	}
	view, err := runtime.Look(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Look != LookCustom || view.Overrides.Dark["primary"] != "150 60% 45%" || view.TextFont != "Inter" || view.CodeFont != "JetBrains Mono" || !view.HasCustom {
		t.Fatalf("view=%+v", view)
	}

	// Choosing a built-in keeps the custom look, so going back needs no
	// redescribing.
	if err := runtime.SetLook(ctx, ports.LookChange{Look: ptr(LookCastIron)}); err != nil {
		t.Fatal(err)
	}
	if view, _ = runtime.Look(ctx); view.Look != LookCastIron || view.Overrides.Dark["background"] != "0 0% 8%" || !view.HasCustom {
		t.Fatalf("view=%+v", view)
	}
	if err := runtime.SetLook(ctx, ports.LookChange{Look: ptr(LookCustom)}); err != nil {
		t.Fatal(err)
	}
	if view, _ = runtime.Look(ctx); view.Look != LookCustom {
		t.Fatalf("view=%+v", view)
	}
	if err := runtime.SetLook(ctx, ports.LookChange{Look: ptr(LookDefault), TextFont: ptr("Geist")}); err != nil {
		t.Fatal(err)
	}
	if view, _ = runtime.Look(ctx); view.Look != LookDefault || len(view.Overrides.Dark) != 0 || view.TextFont != "Geist" {
		t.Fatalf("view=%+v", view)
	}
}

// A refused change stores nothing, and the refusal says what to fix.
func TestSetLookRefusesWhatCannotBePaintedSafely(t *testing.T) {
	runtime, ctx := lookRuntime(t)
	for name, change := range map[string]struct {
		change ports.LookChange
		want   string
	}{
		"unknown token":  {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"background-image": "0 0% 0%"}}}, "not a colour token"},
		"css injection":  {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"primary": "0 0% 0%; } body { display:none"}}}, `must be "H S% L%"`},
		"out of range":   {ports.LookChange{Custom: &ports.PaletteOverrides{Light: map[string]string{"primary": "400 50% 50%"}}}, "out of range"},
		"unreadable":     {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"foreground": "220 10% 10%"}}}, "foreground on background"},
		"empty custom":   {ports.LookChange{Custom: &ports.PaletteOverrides{}}, "needs colours"},
		"unknown look":   {ports.LookChange{Look: ptr("solarized")}, "look must be one of"},
		"no custom yet":  {ports.LookChange{Look: ptr(LookCustom)}, "no custom look yet"},
		"font url":       {ports.LookChange{TextFont: ptr("https://evil.example/font.woff2")}, "text font must be one of"},
		"unoffered font": {ports.LookChange{CodeFont: ptr("Comic Sans")}, "code font must be one of"},
	} {
		err := runtime.SetLook(ctx, change.change)
		if err == nil || !strings.Contains(err.Error(), change.want) {
			t.Fatalf("%s: err=%v, want %q", name, err, change.want)
		}
	}
	view, _ := runtime.Look(ctx)
	if view.Look != LookDefault || view.HasCustom || view.TextFont != "Geist" {
		t.Fatalf("a refused change was stored: %+v", view)
	}
}

func TestSetThemeRefusesAnUnknownMode(t *testing.T) {
	runtime, ctx := lookRuntime(t)
	if err := runtime.SetTheme(ctx, "sepia"); err == nil {
		t.Fatal("expected refusal")
	}
	if err := runtime.SetTheme(ctx, "dark"); err != nil {
		t.Fatal(err)
	}
}
