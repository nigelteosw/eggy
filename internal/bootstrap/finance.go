package bootstrap

import (
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/plugins/finance"
	"github.com/nigelteosw/eggy/internal/ports"
)

// newFinance builds the finance plugin, or nothing at all.
//
// Disabled, it returns a nil service and no tools: no tool schema reaches a
// model call, the panel mounts no route and reports no feature, and the store
// is never touched. That is what makes an unused capability cost nothing, the
// same gate newTavilyTools holds. The table in SQLite exists either way, so
// turning the plugin off never loses what was logged.
//
// The currency list is handed over from config rather than kept by the plugin,
// so config validates the default against the one list the tool, the panel
// dropdown and the service all read.
func newFinance(cfg config.Config, store ports.FinanceStore, location *time.Location, now func() time.Time) (*finance.Service, []ports.Tool, error) {
	if !cfg.Finance.Enabled {
		return nil, nil, nil
	}
	service, err := finance.New(store, finance.Options{
		Currencies: config.FinanceCurrencies,
		Default:    cfg.Finance.Currency,
		Location:   location,
		Now:        now,
	})
	if err != nil {
		return nil, nil, err
	}
	return service, []ports.Tool{finance.NewTool(service)}, nil
}
