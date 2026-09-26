package bootstrap

import (
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/panel"
)

type accountDirectory struct {
	configPath string
	initial    config.Config
}

func newAccountDirectory(configPath string, _ func(string) string, initial config.Config) panel.AccountDirectory {
	return accountDirectory{configPath: configPath, initial: initial}
}

func (d accountDirectory) current() (config.Config, bool) {
	if d.configPath == "" {
		if err := d.initial.Validate(); err != nil {
			return config.Config{}, false
		}
		return d.initial, true
	}
	cfg, err := config.LoadDocument(d.configPath)
	if err != nil || cfg.Validate() != nil {
		return config.Config{}, false
	}
	return cfg, true
}

func accountRecord(account config.AccountConfig) panel.AccountRecord {
	return panel.AccountRecord{ID: account.ID, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID}
}

func (d accountDirectory) Account(id string) (panel.AccountRecord, bool) {
	cfg, valid := d.current()
	if !valid {
		return panel.AccountRecord{}, false
	}
	account, ok := cfg.Account(id)
	if !ok {
		return panel.AccountRecord{}, false
	}
	return accountRecord(account), true
}

// PasswordAccountID is the environment login's binding as the config is
// now. Compared against the boot-time binding on every environment login,
// so a rebinding after boot refuses rather than hands the operator's
// password to someone else.
func (d accountDirectory) PasswordAccountID() string {
	cfg, valid := d.current()
	if !valid {
		return ""
	}
	return cfg.PasswordAccountID()
}

func (d accountDirectory) TelegramEnabled() bool {
	cfg, valid := d.current()
	return valid && cfg.TelegramEnabled()
}

func (d accountDirectory) Accounts() []panel.AccountRecord {
	cfg, valid := d.current()
	if !valid {
		return nil
	}
	accounts := cfg.Principals()
	records := make([]panel.AccountRecord, 0, len(accounts))
	for _, account := range accounts {
		records = append(records, accountRecord(account))
	}
	return records
}
