package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeMarketplace records what /plugin asked the marketplace to install.
type fakeMarketplace struct {
	installed []string
	removed   string
}

func (f *fakeMarketplace) Sources(context.Context) ([]MarketplaceSourceStatus, error) {
	return []MarketplaceSourceStatus{{Name: "skills", Repo: "anthropics/skills", SHA: "abcdef1234"}, {Name: "other", Repo: "o/r", SHA: "1"}}, nil
}
func (f *fakeMarketplace) Detect(_ context.Context, repo string) (string, string, error) {
	if repo == "anthropics/skills" {
		return "claude_marketplace", "", nil
	}
	return "skills_repo", "skills", nil
}
func (f *fakeMarketplace) Items(_ context.Context, source string) ([]MarketplaceItem, error) {
	if source == "other" {
		return []MarketplaceItem{{Name: "shared"}}, nil
	}
	return []MarketplaceItem{{Name: "pdf", Description: "Read PDFs"}, {Name: "shared"}}, nil
}
func (f *fakeMarketplace) Plan(_ context.Context, source, item string) (MarketplacePlan, error) {
	return MarketplacePlan{
		Source: source, Repo: "anthropics/skills", SHA: "abcdef1234", Item: item, Digest: "d1g3st",
		Skills: []MarketplacePlanSkill{{Name: item, Description: "Read PDFs", Body: "Step one: read the PDF."}},
	}, nil
}
func (f *fakeMarketplace) Install(_ context.Context, source, item, sha, digest string) error {
	f.installed = append(f.installed, strings.Join([]string{source, item, sha, digest}, "|"))
	return nil
}
func (f *fakeMarketplace) Installs(context.Context) ([]ports.MarketplaceInstall, error) {
	return []ports.MarketplaceInstall{{ID: "i1", Item: "pdf", SourceName: "skills", CommitSHA: "abcdef1234"}}, nil
}
func (f *fakeMarketplace) Remove(_ context.Context, id string) error { f.removed = id; return nil }

func pluginCtx(kind string, account string) context.Context {
	ctx := destination.With(context.Background(), destination.Destination{Kind: kind})
	return ports.WithPrincipal(ctx, ports.Principal{AccountID: account})
}

var reviewCode = func(reply string) string {
	i := strings.Index(reply, "/plugin approve ")
	if i < 0 {
		return ""
	}
	return strings.Fields(reply[i+len("/plugin approve "):])[0]
}

func TestPluginInstallNeedsApprovedReview(t *testing.T) {
	fake := &fakeMarketplace{}
	svc := New(Options{Marketplace: fake})
	ctx := pluginCtx(destination.Web, "alice")

	card, _, err := svc.Execute(ctx, "/plugin install pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card, "Review: pdf from skills") || !strings.Contains(card, "abcdef1") {
		t.Fatalf("card = %q", card)
	}
	if len(fake.installed) != 0 {
		t.Fatal("install wrote before approval")
	}
	code := reviewCode(card)

	body, _, _ := svc.Execute(ctx, "/plugin show "+code+" pdf")
	if !strings.Contains(body, "Step one") {
		t.Fatalf("show = %q", body)
	}

	// Another account cannot use the code.
	if reply, _, _ := svc.Execute(pluginCtx(destination.Telegram, "bob"), "/plugin approve "+code); !strings.Contains(reply, "no review") {
		t.Fatalf("bob approve = %q", reply)
	}
	// Discord has no decision surface.
	if reply, _, _ := svc.Execute(pluginCtx(destination.Discord, "alice"), "/plugin approve "+code); !strings.Contains(reply, "Discord") {
		t.Fatalf("discord approve = %q", reply)
	}

	reply, _, _ := svc.Execute(ctx, "/plugin approve "+code)
	if !strings.Contains(reply, "Installed pdf") {
		t.Fatalf("approve = %q", reply)
	}
	if want := "skills|pdf|abcdef1234|d1g3st"; len(fake.installed) != 1 || fake.installed[0] != want {
		t.Fatalf("installed = %v, want %s", fake.installed, want)
	}
	// A code is single-use.
	if reply, _, _ := svc.Execute(ctx, "/plugin approve "+code); !strings.Contains(reply, "no review") {
		t.Fatalf("second approve = %q", reply)
	}
}

func TestPluginReviewExpires(t *testing.T) {
	fake := &fakeMarketplace{}
	now := time.Now()
	svc := New(Options{Marketplace: fake, Now: func() time.Time { return now }})
	ctx := pluginCtx(destination.Telegram, "alice")
	card, _, _ := svc.Execute(ctx, "/plugin install pdf@skills")
	now = now.Add(pluginReviewTTL + time.Minute)
	if reply, _, _ := svc.Execute(ctx, "/plugin approve "+reviewCode(card)); !strings.Contains(reply, "no review") {
		t.Fatalf("expired approve = %q", reply)
	}
}

func TestPluginInstallAmbiguousItem(t *testing.T) {
	svc := New(Options{Marketplace: &fakeMarketplace{}})
	reply, _, _ := svc.Execute(pluginCtx(destination.Telegram, "alice"), "/plugin install shared")
	if !strings.Contains(reply, "say which") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestPluginMarketplaceAddWritesSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(browsableConfig()+"\nmarketplace:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(Options{Marketplace: &fakeMarketplace{}, ConfigPath: path})
	ctx := pluginCtx(destination.Telegram, "alice")
	if reply, _, _ := svc.Execute(ctx, "/plugin marketplace add anthropics/skills"); !strings.Contains(reply, "Added marketplace skills") {
		t.Fatalf("add = %q", reply)
	}
	if reply, _, _ := svc.Execute(ctx, "/plugin marketplace add obra/superpowers"); !strings.Contains(reply, "Added") {
		t.Fatalf("add skills repo = %q", reply)
	}
	if reply, _, _ := svc.Execute(ctx, "/plugin marketplace add anthropics/skills"); !strings.Contains(reply, "already added") {
		t.Fatalf("duplicate add = %q", reply)
	}
	sources, err := config.GetMarketplaceSources(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.MarketplaceSource{
		{Name: "skills", Kind: "claude_marketplace", Repo: "anthropics/skills"},
		{Name: "superpowers", Kind: "skills_repo", Repo: "obra/superpowers", Path: "skills"},
	}
	if len(sources) != 2 || sources[0] != want[0] || sources[1] != want[1] {
		t.Fatalf("sources = %+v", sources)
	}
	if reply, _, _ := svc.Execute(ctx, "/plugin marketplace remove skills"); !strings.Contains(reply, "Removed") {
		t.Fatalf("remove = %q", reply)
	}
}

func TestPluginOffWhenExtensionDisabled(t *testing.T) {
	reply, handled, _ := New(Options{}).Execute(pluginCtx(destination.Telegram, "alice"), "/plugin list")
	if !handled || !strings.Contains(reply, "marketplace is off") {
		t.Fatalf("reply = %q", reply)
	}
}
