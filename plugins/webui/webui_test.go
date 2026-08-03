package webui

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetsServesTheRealBuildOrThePlaceholder(t *testing.T) {
	data, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatalf("expected index.html to resolve: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty index.html")
	}
}

// The placeholder is what a clone without a built bundle serves, so it may not
// reference the hashed assets that only exist after `make build-web` -- a page
// naming files that are not there is the failure this arrangement replaced.
func TestPlaceholderReferencesNoBuiltAssets(t *testing.T) {
	data, err := fs.ReadFile(Assets(), "placeholder.html")
	if err != nil {
		t.Fatalf("expected placeholder.html to be embedded: %v", err)
	}
	if strings.Contains(string(data), "/assets/") {
		t.Error("placeholder references a built asset path")
	}
}

func TestPlaceholderStandsInForAMissingIndex(t *testing.T) {
	assets := placeholderFS{fstest.MapFS{"placeholder.html": {Data: []byte("unbuilt")}}}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("expected the placeholder to stand in for index.html: %v", err)
	}
	if string(data) != "unbuilt" {
		t.Fatalf("index.html = %q, want the placeholder", data)
	}
}

func TestBuiltIndexWinsOverThePlaceholder(t *testing.T) {
	assets := placeholderFS{fstest.MapFS{
		"index.html":       {Data: []byte("built")},
		"placeholder.html": {Data: []byte("unbuilt")},
	}}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "built" {
		t.Fatalf("index.html = %q, want the real build", data)
	}
}

// A missing asset must stay missing: substituting the placeholder for anything
// other than index.html would answer a 404 with an HTML page.
func TestMissingAssetIsNotSubstituted(t *testing.T) {
	assets := placeholderFS{fstest.MapFS{"placeholder.html": {Data: []byte("unbuilt")}}}
	if _, err := fs.ReadFile(assets, "assets/index-abc123.js"); err == nil {
		t.Fatal("expected a missing asset to stay missing")
	}
}
