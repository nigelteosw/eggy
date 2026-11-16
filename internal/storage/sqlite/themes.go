package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// themeSchema holds the themes people save for everyone, beside the built-in
// ones the binary embeds. It is additive and applied on every open, so an
// older binary opens the file and ignores it, and no MachineStateVersion bump
// is needed. Palettes are stored as JSON objects of token to OKLCH triple,
// already checked by the look service before they reach here.
const themeSchema = `
CREATE TABLE IF NOT EXISTS user_themes (
  name        TEXT PRIMARY KEY,
  label       TEXT NOT NULL,
  description TEXT NOT NULL,
  light       TEXT NOT NULL,
  dark        TEXT NOT NULL,
  created_by  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
`

func (s *Store) ListThemes(ctx context.Context) ([]ports.UserTheme, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, label, description, light, dark, created_by, created_at
		FROM user_themes ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var themes []ports.UserTheme
	for rows.Next() {
		var theme ports.UserTheme
		var light, dark, created string
		if err := rows.Scan(&theme.Name, &theme.Label, &theme.Description, &light, &dark, &theme.CreatedBy, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(light), &theme.Light); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(dark), &theme.Dark); err != nil {
			return nil, err
		}
		theme.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		themes = append(themes, theme)
	}
	return themes, rows.Err()
}

func (s *Store) CreateTheme(ctx context.Context, theme ports.UserTheme) error {
	light, err := json.Marshal(nonNil(theme.Light))
	if err != nil {
		return err
	}
	dark, err := json.Marshal(nonNil(theme.Dark))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_themes (name, label, description, light, dark, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		theme.Name, theme.Label, theme.Description, string(light), string(dark), theme.CreatedBy, theme.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: user_themes.name") {
		return ports.ErrThemeExists
	}
	return err
}

func (s *Store) DeleteTheme(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_themes WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ports.ErrThemeNotFound
	}
	return nil
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

var _ ports.ThemeStore = (*Store)(nil)
