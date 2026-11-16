package services

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

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
	custom := ports.PaletteOverrides{Dark: map[string]string{"primary": "62% 0.15 155", "accent-600": "62% 0.15 155"}}
	if err := runtime.SetLook(ctx, ports.LookChange{Custom: &custom, TextFont: ptr("Inter")}); err != nil {
		t.Fatal(err)
	}
	view, err := runtime.Look(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Look != LookCustom || view.Overrides.Dark["primary"] != "62% 0.15 155" || view.TextFont != "Inter" || view.CodeFont != "JetBrains Mono" || !view.HasCustom {
		t.Fatalf("view=%+v", view)
	}

	// Choosing a built-in keeps the custom look, so going back needs no
	// redescribing.
	if err := runtime.SetLook(ctx, ports.LookChange{Look: ptr("cast-iron")}); err != nil {
		t.Fatal(err)
	}
	if view, _ = runtime.Look(ctx); view.Look != "cast-iron" || view.Overrides.Dark["background"] != "19.3% 0 0" || !view.HasCustom {
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
		"unknown token":  {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"background-image": "0% 0 0"}}}, "not a colour token"},
		"css injection":  {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"primary": "0% 0 0; } body { display:none"}}}, `must be OKLCH`},
		"out of range":   {ports.LookChange{Custom: &ports.PaletteOverrides{Light: map[string]string{"primary": "50% 0.9 120"}}}, "out of range"},
		"unreadable":     {ports.LookChange{Custom: &ports.PaletteOverrides{Dark: map[string]string{"foreground": "20% 0.01 260"}}}, "foreground on background"},
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

// Black and white are 21:1 in any colour space; OKLCH must arrive at the same
// ratio WCAG defines over sRGB, or every contrast check is off.
func TestOKLCHContrastMatchesWCAG(t *testing.T) {
	if ratio := contrastRatio([3]float64{100, 0, 0}, [3]float64{0, 0, 0}); ratio < 20.9 || ratio > 21.1 {
		t.Fatalf("white on black = %.2f, want 21", ratio)
	}
}

// A palette stored as HSL before the move to OKLCH still paints, converted on
// the way out, and the stored copy is left as it was.
func TestALegacyHSLCustomLookIsServedAsOKLCH(t *testing.T) {
	runtime, ctx := lookRuntime(t)
	legacy := map[string]string{"primary": "33 81% 54%", "background": "0 0% 100%"}
	if err := runtime.update(ctx, func(state *ports.State) {
		state.Agent.CustomLook = &ports.PaletteOverrides{Dark: legacy}
		state.Agent.Look = LookCustom
	}); err != nil {
		t.Fatal(err)
	}
	view, err := runtime.Look(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Overrides.Dark["primary"]; got != "73.6% 0.151 65.6" {
		t.Fatalf("primary = %q", got)
	}
	if got := view.Overrides.Dark["background"]; got != "100% 0 0" {
		t.Fatalf("background = %q", got)
	}
	if legacy["primary"] != "33 81% 54%" {
		t.Fatal("conversion rewrote the stored look")
	}
}

// The panel draws every look from what the server sends, so each theme in
// themes.json arrives with its words and swatch colours, in file order, and
// custom only once the account has one.
func TestLookOptionsComeFromTheThemesFile(t *testing.T) {
	runtime, ctx := lookRuntime(t)
	view, err := runtime.Look(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, option := range view.Options {
		names = append(names, option.Name)
		if option.Label == "" || option.Light == "" || option.Dark == "" || option.Accent == "" {
			t.Fatalf("incomplete option %+v", option)
		}
	}
	if !slices.Equal(names, []string{"eggshell", "cast-iron", "lavender"}) {
		t.Fatalf("options = %v", names)
	}
	if view.Options[2].Light != "97% 0.012 300" {
		t.Fatalf("lavender light = %q", view.Options[2].Light)
	}
}

// A custom look saved as a theme is offered to every account and selected for
// the one that saved it; only that account may delete it, and anyone using it
// then falls back to the default.
func TestSavedThemesAreSharedAndOwnedByTheirSaver(t *testing.T) {
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	runtime := NewAgentRuntime(database.State(), "fast", []string{"fast"}, nil)
	runtime.UseThemes(database)
	alice := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "alice"})
	bob := ports.WithPrincipal(context.Background(), ports.Principal{AccountID: "bob"})

	if _, err := runtime.SaveTheme(alice, "Mint", ""); err == nil || !strings.Contains(err.Error(), "no custom look") {
		t.Fatalf("save without a custom look: %v", err)
	}
	custom := ports.PaletteOverrides{Dark: map[string]string{"primary": "62% 0.15 155"}}
	if err := runtime.SetLook(alice, ports.LookChange{Custom: &custom}); err != nil {
		t.Fatal(err)
	}
	name, err := runtime.SaveTheme(alice, "Mint Julep!", "Cool green.")
	if err != nil || name != "mint-julep" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := runtime.SaveTheme(bob, "mint julep", ""); !errors.Is(err, ports.ErrThemeExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := runtime.SaveTheme(alice, "Lavender", ""); !errors.Is(err, ports.ErrThemeExists) {
		t.Fatalf("built-in name: %v", err)
	}

	view, _ := runtime.Look(alice)
	if view.Look != "mint-julep" || view.Overrides.Dark["primary"] != "62% 0.15 155" {
		t.Fatalf("alice view=%+v", view)
	}
	if err := runtime.SetLook(bob, ports.LookChange{Look: ptr("mint-julep")}); err != nil {
		t.Fatal(err)
	}
	view, _ = runtime.Look(bob)
	last := view.Options[len(view.Options)-1]
	if view.Look != "mint-julep" || last.Name != "mint-julep" || !last.User || last.Mine {
		t.Fatalf("bob view=%+v", view)
	}

	if err := runtime.DeleteTheme(bob, "mint-julep"); err == nil {
		t.Fatal("bob deleted alice's theme")
	}
	if err := runtime.DeleteTheme(alice, "lavender"); err == nil {
		t.Fatal("deleted a built-in")
	}
	if err := runtime.DeleteTheme(alice, "mint-julep"); err != nil {
		t.Fatal(err)
	}
	if view, _ = runtime.Look(bob); view.Look != LookDefault {
		t.Fatalf("bob kept a deleted theme: %+v", view.Look)
	}
}
