package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

var _ ports.MarketplaceStore = (*Store)(nil)

func marketplaceInstall(id, item string) ports.MarketplaceInstall {
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	return ports.MarketplaceInstall{
		ID: id, SourceName: "anthropic", Repo: "anthropics/skills", Item: item,
		CommitSHA: "aaaa", PlanDigest: "dig-" + id, InstalledBy: "a",
		InstalledAt: at, UpdatedAt: at,
		Files: []ports.MarketplaceFile{
			{Kind: "skill", Target: item, ContentHash: "h1"},
			{Kind: "mcp_server", Target: item + "-api", ContentHash: "h2"},
		},
	}
}

func TestMarketplaceInstallRoundTrip(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	want := marketplaceInstall("i1", "pdf")
	if err := db.CreateInstall(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetInstall(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceName != want.SourceName || got.Item != want.Item || got.CommitSHA != want.CommitSHA ||
		got.PlanDigest != want.PlanDigest || got.InstalledBy != want.InstalledBy || got.Disabled {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if !got.InstalledAt.Equal(want.InstalledAt) || len(got.Files) != 2 {
		t.Fatalf("times or files lost: %+v", got)
	}
	list, err := db.ListInstalls(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err %v", list, err)
	}
}

func TestMarketplaceInstallUnique(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.CreateInstall(ctx, marketplaceInstall("i1", "pdf")); err != nil {
		t.Fatal(err)
	}
	err := db.CreateInstall(ctx, marketplaceInstall("i2", "pdf"))
	if !errors.Is(err, ports.ErrMarketplaceInstallExists) {
		t.Fatalf("second install err = %v, want ErrMarketplaceInstallExists", err)
	}
	if _, err := db.GetInstall(ctx, "i2"); !errors.Is(err, ports.ErrMarketplaceInstallNotFound) {
		t.Fatalf("refused install left a record: %v", err)
	}
}

func TestMarketplaceReplaceInstallSwapsFiles(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.CreateInstall(ctx, marketplaceInstall("i1", "pdf")); err != nil {
		t.Fatal(err)
	}
	updated := marketplaceInstall("i1", "pdf")
	updated.CommitSHA = "bbbb"
	updated.Files = []ports.MarketplaceFile{{Kind: "skill", Target: "pdf", ContentHash: "h3"}}
	if err := db.ReplaceInstall(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetInstall(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CommitSHA != "bbbb" || len(got.Files) != 1 || got.Files[0].ContentHash != "h3" {
		t.Fatalf("replaced install = %+v", got)
	}
}

func TestMarketplaceDeleteRemovesFiles(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	if err := db.CreateInstall(ctx, marketplaceInstall("i1", "pdf")); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteInstall(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetInstall(ctx, "i1"); !errors.Is(err, ports.ErrMarketplaceInstallNotFound) {
		t.Fatalf("get after delete err = %v", err)
	}
	var files int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM marketplace_files WHERE install_id = ?`, "i1").Scan(&files); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("%d file rows survived delete", files)
	}
}

func TestMarketplaceSetDisabledAndEmptyStore(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := context.Background()
	list, err := db.ListInstalls(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty store list = %v err %v", list, err)
	}
	if err := db.CreateInstall(ctx, marketplaceInstall("i1", "pdf")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetInstallDisabled(ctx, "i1", true); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetInstall(ctx, "i1")
	if !got.Disabled {
		t.Fatal("disable not recorded")
	}
}
