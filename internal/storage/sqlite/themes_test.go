package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// User themes outlive a redeploy because they live in eggy.db: one saved
// through one open store is read back through the next.
func TestUserThemesPersistAcrossReopens(t *testing.T) {
	path := t.TempDir() + "/eggy.db"
	ctx := context.Background()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	theme := ports.UserTheme{Name: "mint", Label: "Mint", Dark: map[string]string{"primary": "80% 0.1 160"}, CreatedBy: "42", CreatedAt: time.Now()}
	if err := store.CreateTheme(ctx, theme); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTheme(ctx, theme); !errors.Is(err, ports.ErrThemeExists) {
		t.Fatalf("second create = %v, want ErrThemeExists", err)
	}
	_ = store.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	themes, err := reopened.ListThemes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(themes) != 1 || themes[0].Label != "Mint" || themes[0].Dark["primary"] != "80% 0.1 160" || themes[0].Light == nil || themes[0].CreatedBy != "42" {
		t.Fatalf("themes = %+v", themes)
	}
	if err := reopened.DeleteTheme(ctx, "mint"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteTheme(ctx, "mint"); !errors.Is(err, ports.ErrThemeNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
