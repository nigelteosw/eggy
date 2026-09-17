package bootstrap

import (
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/web"
)

type accountDirectory struct {
	configPath string
	initial    config.Config
}

func newAccountDirectory(configPath string, _ func(string) string, initial config.Config) web.AccountDirectory {
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

func accountRecord(account config.AccountConfig) web.AccountRecord {
	return web.AccountRecord{ID: account.ID, Email: account.GoogleEmail, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID}
}

func (d accountDirectory) Account(id string) (web.AccountRecord, bool) {
	cfg, valid := d.current()
	if !valid {
		return web.AccountRecord{}, false
	}
	account, ok := cfg.Account(id)
	if !ok {
		return web.AccountRecord{}, false
	}
	return accountRecord(account), true
}

func (d accountDirectory) AccountForEmail(email string) (web.AccountRecord, bool) {
	cfg, valid := d.current()
	if !valid {
		return web.AccountRecord{}, false
	}
	account, ok := cfg.AccountForEmail(email)
	if !ok {
		return web.AccountRecord{}, false
	}
	return accountRecord(account), true
}

func (d accountDirectory) Accounts() []web.AccountRecord {
	cfg, valid := d.current()
	if !valid {
		return nil
	}
	accounts := cfg.Principals()
	records := make([]web.AccountRecord, 0, len(accounts))
	for _, account := range accounts {
		records = append(records, accountRecord(account))
	}
	return records
}
