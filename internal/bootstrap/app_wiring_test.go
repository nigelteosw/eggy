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
