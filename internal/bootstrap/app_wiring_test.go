package bootstrap

import (
	"context"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

type listableModel struct{ staticModel }

func (listableModel) ListModels(context.Context) ([]ports.CatalogModel, error) {
	return []ports.CatalogModel{{ID: "openai/gpt-5"}}, nil
}

type catalogModel struct {
	staticModel
	models []ports.CatalogModel
}

func (c catalogModel) ListModels(context.Context) ([]ports.CatalogModel, error) { return c.models, nil }

// Two things make a provider browsable and both are required: it opted in, and
// its adapter can actually list. A provider failing either is simply absent,
// which is the panel's "cannot be browsed" rather than an error.
func TestModelDiscoveryHonoursTheOptOutAndTheAdapterCapability(t *testing.T) {
	off := false
	on := true
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"listable":  {Adapter: "openai_compatible"},
		"explicit":  {Adapter: "openai_compatible", DiscoverModels: &on},
		"optedout":  {Adapter: "openai_compatible", DiscoverModels: &off},
		"nocatalog": {Adapter: "openai_compatible"},
	}}
	adapters := map[string]ports.Model{
		"listable":  listableModel{},
		"explicit":  listableModel{},
		"optedout":  listableModel{},
		"nocatalog": staticModel{},
	}
	discovery := newModelDiscovery(cfg, adapters)

	got := discovery.DiscoverableProviders()
	want := []string{"explicit", "listable"}
	if len(got) != len(want) {
		t.Fatalf("providers=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("providers=%v want=%v", got, want)
		}
	}
	if models, err := discovery.DiscoverModels(context.Background(), "listable"); err != nil || len(models) != 1 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	// Asking anyway must fail rather than fall back to some other provider.
	if _, err := discovery.DiscoverModels(context.Background(), "optedout"); err == nil {
		t.Fatal("an opted-out provider must not be listable")
	}
	if _, err := discovery.DiscoverModels(context.Background(), "nocatalog"); err == nil {
		t.Fatal("an adapter that cannot list must not be listable")
	}
}

// Image support is known only when the provider both opted into discovery and
// said so for the exact model the alias names. Everything else -- an opted-out
// provider, a model the catalog does not carry, an entry with no architecture,
// or an alias that does not exist -- is unknown, and the turn sends the image
// anyway rather than blocking on a guess.
func TestModelDiscoveryAnswersImageSupportForAnAlias(t *testing.T) {
	images, textOnly, off := true, false, false
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"openrouter": {Adapter: "openai_compatible", BaseURL: "https://openrouter.ai/api/v1"},
			// A provider that is not OpenRouter never publishes modalities, so
			// even a catalog entry claiming them must stay unknown: the turn
			// path must not spend a request to ask.
			"plain":    {Adapter: "openai_compatible", BaseURL: "https://api.example/v1"},
			"optedout": {Adapter: "openai_compatible", BaseURL: "https://openrouter.ai/api/v1", DiscoverModels: &off},
		},
		ModelAliases: map[string]config.ModelAliasConfig{
			"vision":   {Provider: "openrouter", Model: "vendor/vision"},
			"text":     {Provider: "openrouter", Model: "vendor/text"},
			"silent":   {Provider: "openrouter", Model: "vendor/silent"},
			"absent":   {Provider: "openrouter", Model: "vendor/absent"},
			"unlisted": {Provider: "optedout", Model: "vendor/vision"},
			"proxied":  {Provider: "plain", Model: "vendor/vision"},
		},
	}
	catalog := catalogModel{models: []ports.CatalogModel{
		{ID: "vendor/vision", SupportsImages: &images, SupportsFiles: &textOnly},
		{ID: "vendor/text", SupportsImages: &textOnly, SupportsFiles: &textOnly},
		{ID: "vendor/silent"},
	}}
	discovery := newModelDiscovery(cfg, map[string]ports.Model{"openrouter": catalog, "plain": catalog, "optedout": catalog})

	cases := map[string]struct{ supported, known bool }{
		"vision":   {true, true},
		"text":     {false, true},
		"silent":   {false, false},
		"absent":   {false, false},
		"unlisted": {false, false},
		"proxied":  {false, false},
		"missing":  {false, false},
	}
	for alias, want := range cases {
		supported, known := discovery.SupportsPart(context.Background(), alias, ports.ContentTypeImage)
		if supported != want.supported || known != want.known {
			t.Fatalf("SupportsPart(%q, image)=%v,%v want %v,%v", alias, supported, known, want.supported, want.known)
		}
	}
	// Files are answered from their own field: the vision model sees images
	// and is still a known "no" for a PDF.
	if supported, known := discovery.SupportsPart(context.Background(), "vision", ports.ContentTypeDocument); supported || !known {
		t.Fatalf("SupportsPart(vision, document)=%v,%v want false,true", supported, known)
	}
	if _, known := discovery.SupportsPart(context.Background(), "silent", ports.ContentTypeDocument); known {
		t.Fatal("a model with no architecture must leave file support unknown")
	}
}
