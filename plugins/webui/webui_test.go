package webui

import (
	"io/fs"
	"testing"
)

func TestAssetsServesThePlaceholderOrRealBuild(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("expected index.html to be embedded: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty index.html")
	}
}
