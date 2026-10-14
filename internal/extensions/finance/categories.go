package finance

import (
	"context"
	"hash/fnv"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// palette is the colours a category may take. The page maps each name to its own
// shades for light and dark, so a name here is a promise the panel can draw. A
// free hex value is refused rather than drawn, because a colour the page cannot
// show in both themes would be unreadable in one of them.
var palette = []string{"sky", "emerald", "amber", "rose", "violet", "orange", "teal", "indigo", "lime", "slate"}

// Palette is a copy of the colours a category may take, for the panel's swatches.
func (s *Service) Palette() []string { return slices.Clone(palette) }

// defaultColor is the colour a category draws in until one is chosen. It is
// worked out from the name, so a category keeps the same colour on every
// device and across restarts without anything being stored.
func defaultColor(name string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return palette[hash.Sum32()%uint32(len(palette))]
}

// Categories lists the account's categories, each with the colour it draws in:
// the one chosen for it, or its default.
func (s *Service) Categories(ctx context.Context) ([]ports.FinanceCategory, error) {
	categories, err := s.store.FinanceCategories(ctx)
	if err != nil {
		return nil, err
	}
	for i := range categories {
		if categories[i].Color == "" {
			categories[i].Color = defaultColor(categories[i].Name)
		}
	}
	return categories, nil
}

// SetCategoryColor chooses a category's colour from the palette.
func (s *Service) SetCategoryColor(ctx context.Context, name, color string) error {
	name, err := cleanCategory(name)
	if err != nil {
		return err
	}
	color = strings.ToLower(strings.TrimSpace(color))
	if !slices.Contains(palette, color) {
		return invalid("colour %q must be one of %s", color, strings.Join(palette, ", "))
	}
	return s.store.SetFinanceCategoryColor(ctx, name, color)
}

// RenameCategory moves every entry in one category into another. Naming a
// category after one that already exists merges them, and the existing colour
// wins, so the owner's choice for the target is the one that stays.
func (s *Service) RenameCategory(ctx context.Context, from, to string) error {
	from, err := cleanCategory(from)
	if err != nil {
		return err
	}
	to, err = cleanCategory(to)
	if err != nil {
		return err
	}
	if from == to {
		return invalid("%s is already named %s", from, to)
	}
	categories, err := s.store.FinanceCategories(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(categories, func(c ports.FinanceCategory) bool { return c.Name == from }) {
		return invalid("there is no category named %q", from)
	}
	return s.store.RenameFinanceCategory(ctx, from, to)
}
