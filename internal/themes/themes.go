// Package themes is the one place Eggy's colours are written: themes.json,
// an ordered list of looks. The server embeds it to offer and check looks,
// and the panel's Tailwind build reads the same file to write the
// stylesheet's :root and .dark palettes, so no colour is repeated anywhere.
//
// The first theme is the default. It must give every token for both light
// and dark, and its tokens are the whole set a look may use. Every other
// theme overrides some of them, for light, dark or both; a mode it leaves out
// is the default's. Adding a theme is adding an entry: the panel lists it from
// what the server sends.
//
// Every value is an OKLCH triple, "L% C H". border is the ink hairlines are
// drawn in; the stylesheet lays it at low alpha over whatever is behind it.
package themes

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
)

//go:embed themes.json
var raw []byte

// Theme is one entry of themes.json.
type Theme struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Light       map[string]string `json:"light,omitempty"`
	Dark        map[string]string `json:"dark,omitempty"`
}

var (
	once   sync.Once
	loaded []Theme
	err    error
)

// All returns every theme in file order, the default first. The file is
// embedded, so a malformed one is a build defect: a test calls Check, and All
// panics rather than serve a panel with no palette.
func All() []Theme {
	once.Do(func() { loaded, err = parse(raw) })
	if err != nil {
		panic(err)
	}
	return slices.Clone(loaded)
}

// Check parses the embedded file and reports what is wrong with it.
func Check() error {
	_, err := parse(raw)
	return err
}

func parse(data []byte) ([]Theme, error) {
	var list []Theme
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("themes.json: %w", err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("themes.json: no themes")
	}
	base := list[0]
	tokens := slices.Sorted(maps.Keys(base.Light))
	if len(tokens) == 0 || !slices.Equal(tokens, slices.Sorted(maps.Keys(base.Dark))) {
		return nil, fmt.Errorf("themes.json: the default %q must give the same tokens for light and dark", base.Name)
	}
	seen := map[string]bool{}
	for _, theme := range list {
		if theme.Name == "" || theme.Label == "" {
			return nil, fmt.Errorf("themes.json: every theme needs a name and a label")
		}
		if seen[theme.Name] || theme.Name == "custom" {
			return nil, fmt.Errorf("themes.json: theme name %q is taken", theme.Name)
		}
		seen[theme.Name] = true
		for _, mode := range []map[string]string{theme.Light, theme.Dark} {
			for name := range mode {
				if !slices.Contains(tokens, name) {
					return nil, fmt.Errorf("themes.json: %s sets %q, which the default does not define", theme.Name, name)
				}
			}
		}
	}
	return list, nil
}
