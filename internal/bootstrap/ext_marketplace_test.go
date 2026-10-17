package bootstrap

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/extensions/marketplace"
	"github.com/nigelteosw/eggy/internal/panel"
)

func TestMarketplaceDisabledIsNil(t *testing.T) {
	svc, err := newMarketplace(config.Config{}, "", nil, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if svc != nil {
		t.Fatal("marketplace built while marketplace.enabled is off")
	}
}

func TestMarketplaceErrorMapping(t *testing.T) {
	reset := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"digest", marketplace.ErrDigestMismatch, panel.ErrMarketplaceDigest},
		{"conflict", marketplace.ConflictError{Conflicts: []string{"pdf"}}, panel.MarketplaceConflictError{Conflicts: []string{"pdf"}}},
		{"wrapped conflict", fmt.Errorf("plan: %w", marketplace.ConflictError{Conflicts: []string{"pdf"}}), panel.MarketplaceConflictError{Conflicts: []string{"pdf"}}},
		{"rate limit", marketplace.RateLimitError{Reset: reset}, panel.MarketplaceRateLimitError{Reset: reset}},
		{"archive", fmt.Errorf("%w: symlink x", marketplace.ErrArchiveRejected), panel.ErrMarketplaceArchive},
	}
	for _, tc := range cases {
		got := translateMarketplaceError(tc.in)
		if !errors.Is(got, tc.want) && !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestMarketplaceErrorPassesThroughUnknown(t *testing.T) {
	sentinel := errors.New("something else")
	if got := translateMarketplaceError(sentinel); got != sentinel {
		t.Fatalf("unknown error changed: %v", got)
	}
}
