package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func marketplaceConfigPath(t *testing.T, extra string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMarketplaceConfigValidates(t *testing.T) {
	cases := map[string]string{
		"bad kind":       "\nmarketplace:\n  enabled: true\n  sources:\n    - name: a\n      kind: git\n      repo: o/r\n",
		"duplicate name": "\nmarketplace:\n  enabled: true\n  sources:\n    - name: a\n      kind: skills_repo\n      repo: o/r\n    - name: a\n      kind: skills_repo\n      repo: o/s\n",
		"path escapes":   "\nmarketplace:\n  enabled: true\n  sources:\n    - name: a\n      kind: skills_repo\n      repo: o/r\n      path: ../x\n",
		"not a repo":     "\nmarketplace:\n  enabled: true\n  sources:\n    - name: a\n      kind: skills_repo\n      repo: notarepo\n",
		"bad name":       "\nmarketplace:\n  enabled: true\n  sources:\n    - name: Bad Name\n      kind: skills_repo\n      repo: o/r\n",
	}
	for label, extra := range cases {
		cfg, err := LoadDocument(marketplaceConfigPath(t, extra))
		if err != nil {
			t.Fatalf("%s: load: %v", label, err)
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: Validate accepted the section", label)
		}
	}
}

func TestMarketplaceConfigAcceptsValidSources(t *testing.T) {
	extra := "\nmarketplace:\n  enabled: true\n  sources:\n    - name: anthropic-skills\n      kind: skills_repo\n      repo: anthropics/skills\n      path: skills\n    - name: team\n      kind: claude_marketplace\n      repo: example-org/claude-plugins\n      ref: main\n"
	cfg, err := LoadDocument(marketplaceConfigPath(t, extra))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid sources refused: %v", err)
	}
}

func TestSetMarketplaceRoundTrip(t *testing.T) {
	path := marketplaceConfigPath(t, "")
	sources := []MarketplaceSource{{Name: "anthropic-skills", Kind: "skills_repo", Repo: "anthropics/skills", Path: "skills"}}
	if err := SetMarketplace(path, true, sources); err != nil {
		t.Fatal(err)
	}
	got, err := GetMarketplaceSources(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, sources) {
		t.Fatalf("sources = %+v, want %+v", got, sources)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Marketplace.Enabled {
		t.Fatal("enabled not saved")
	}
}

func TestSetMarketplaceRefusesInvalidSourceAndLeavesFile(t *testing.T) {
	path := marketplaceConfigPath(t, "")
	before, _ := os.ReadFile(path)
	bad := []MarketplaceSource{{Name: "a", Kind: "git", Repo: "o/r"}}
	if err := SetMarketplace(path, true, bad); err == nil {
		t.Fatal("accepted a bad kind")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a refused save changed the file")
	}
}

func TestMarketplaceSourcesEmptyByDefault(t *testing.T) {
	path := marketplaceConfigPath(t, "")
	got, err := GetMarketplaceSources(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("default sources = %+v, want none", got)
	}
}
