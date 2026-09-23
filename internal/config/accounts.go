// Accounts: who may use this deployment. Two shapes describe the same thing.
// The legacy shape is one owner.id (or a telegram.owner_id it is derived
// from); the explicit shape is an accounts list, each entry naming an
// immutable ID and, optionally, the Telegram sender that speaks for it. Both
// normalize to Principals, so the runtime only ever sees a list of accounts
// and never asks which shape it came from.
//
// How a person signs in to the web panel is not in this file's YAML at all:
// one account, web.password_account_id, uses the operator's environment
// credentials; every other account has a local password held as a machine
// record in SQLite. Membership is YAML, credentials are not.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AccountConfig is one person. There is no role field, and none is coming:
// every account holds every capability, and what separates accounts is
// ownership of private records, not permission.
type AccountConfig struct {
	ID string `yaml:"id"`
	// TelegramUserID is the numeric sender that maps onto this account. Zero
	// means this person does not use Telegram. Never a username: those are
	// reassignable.
	TelegramUserID int64 `yaml:"telegram_user_id,omitempty"`
	// DiscordUserID is the opaque Discord user that maps onto this account,
	// bound by redeeming a linking token in a DM. Empty means this person does
	// not use Discord.
	DiscordUserID string `yaml:"discord_user_id,omitempty"`
}

// WebConfig holds the inbound login binding. The only thing YAML says about
// web login is which account the operator's environment credentials
// (EGGY_UI_USER_EMAIL / EGGY_UI_PASSWORD) sign in; every other credential is
// a SQLite record.
type WebConfig struct {
	// PasswordAccountID names the account bound to the environment
	// credentials. Required with an accounts list -- list order never
	// decides it -- and implied by owner.id in the legacy shape.
	PasswordAccountID string `yaml:"password_account_id,omitempty"`
}

// AccountMode reports whether the deployment authenticates people through the
// explicit accounts list. The migration mapping only makes sense with that
// list, so it selects the mode too, as does a password binding with no
// legacy owner to bind: a config that names one and forgets the list is
// reported as missing the list rather than silently treated as a legacy
// owner.
func (c Config) AccountMode() bool {
	if len(c.Accounts) > 0 || strings.TrimSpace(c.MigrationOwnerID) != "" {
		return true
	}
	return strings.TrimSpace(c.Web.PasswordAccountID) != "" && strings.TrimSpace(c.Owner.ID) == "" && c.Telegram.OwnerID == 0
}

// PasswordAccountID is the account the environment credentials sign in:
// the explicit binding, or the legacy owner, who is the only account there
// is. Empty only for a document validation has already refused.
func (c Config) PasswordAccountID() string {
	if id := strings.TrimSpace(c.Web.PasswordAccountID); id != "" {
		return id
	}
	if !c.AccountMode() {
		return strings.TrimSpace(c.Owner.ID)
	}
	return ""
}

// AccountForUsername resolves what a person typed into the login form: an
// account ID exactly as stored (after trimming), or the environment alias,
// which is reserved case-insensitively for the password account. The alias
// is compared case-insensitively because it is an address-shaped string
// people type from memory; IDs are not, because they name directories.
func (c Config) AccountForUsername(username, environmentAlias string) (AccountConfig, bool) {
	username = strings.TrimSpace(username)
	if username == "" {
		return AccountConfig{}, false
	}
	if alias := strings.TrimSpace(environmentAlias); alias != "" && strings.EqualFold(username, alias) {
		return c.Account(c.PasswordAccountID())
	}
	return c.Account(username)
}

// Principals is the list of accounts the runtime authorizes against, in the
// order the owner wrote them. A legacy owner is one account whose ID is the
// owner.id and whose Telegram sender is telegram.owner_id, so the rest of
// the program has exactly one shape to read.
func (c Config) Principals() []AccountConfig {
	if c.AccountMode() {
		return c.Accounts
	}
	if strings.TrimSpace(c.Owner.ID) == "" {
		return nil
	}
	return []AccountConfig{{ID: c.Owner.ID, TelegramUserID: c.Telegram.OwnerID}}
}

// Account resolves a configured account by ID. It is the one lookup every
// ingress uses to decide whether an identity still exists: a removed account
// stops resolving here, and everything that depends on it fails closed.
func (c Config) Account(id string) (AccountConfig, bool) {
	for _, account := range c.Principals() {
		if account.ID == id {
			return account, true
		}
	}
	return AccountConfig{}, false
}

// AccountForTelegram resolves the account a verified numeric Telegram sender
// speaks for. Unmapped senders resolve to nothing, and nothing is the answer:
// there is no first-configured-user fallback.
func (c Config) AccountForTelegram(userID int64) (AccountConfig, bool) {
	if userID <= 0 {
		return AccountConfig{}, false
	}
	for _, account := range c.Principals() {
		if account.TelegramUserID == userID {
			return account, true
		}
	}
	return AccountConfig{}, false
}

// TelegramEnabled reports whether Telegram is a channel for this deployment:
// whether any account can be reached there. It replaces reading
// telegram.owner_id directly, which in account mode is always zero.
func (c Config) TelegramEnabled() bool {
	if c.Telegram.Enabled != nil {
		return *c.Telegram.Enabled
	}
	for _, account := range c.Principals() {
		if account.TelegramUserID != 0 {
			return true
		}
	}
	return false
}

// normalizeEmail is the one spelling rule for an address: trimmed and
// lowercased, nothing more. An empty result means no address.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail is deliberately loose: one @ with something on each side. Google
// decides what an address is; this only catches a value that cannot be one.
func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && !strings.ContainsAny(email, " \t\r\n") && strings.Count(email, "@") == 1
}

// normalizeAccounts applies the spelling rules in place so every reader sees
// canonical values, the same way Google product names are canonicalized.
func (c *Config) normalizeAccounts() {
	for i := range c.Accounts {
		c.Accounts[i].ID = strings.TrimSpace(c.Accounts[i].ID)
	}
	c.Google.ExpectedEmail = normalizeEmail(c.Google.ExpectedEmail)
	c.MigrationOwnerID = strings.TrimSpace(c.MigrationOwnerID)
	c.Web.PasswordAccountID = strings.TrimSpace(c.Web.PasswordAccountID)
}

// validateAccounts is every rule that decides whether the account list is
// usable. It runs on both shapes: legacy checks are here too, so the two ways
// of naming the owner cannot be mixed into a document that means two things.
func (c Config) validateAccounts() error {
	if !c.AccountMode() {
		if strings.TrimSpace(c.Owner.ID) == "" {
			return errors.New("owner.id must be set")
		}
		// owner.id is the system-wide identity; Telegram is one optional
		// channel onto it. A negative owner_id is a typo rather than an
		// omission, so it is still rejected -- but omitting the block
		// entirely is a web-only deployment, not an error.
		if c.Telegram.OwnerID < 0 {
			return errors.New("telegram.owner_id must be positive when set")
		}
		if c.Telegram.Configured() && strconv.FormatInt(c.Telegram.OwnerID, 10) != c.Owner.ID {
			return errors.New("owner.id must match telegram.owner_id when Telegram is configured")
		}
		if c.Telegram.Enabled != nil && *c.Telegram.Enabled && c.Telegram.OwnerID == 0 {
			return errors.New("telegram.enabled requires telegram.owner_id outside account mode")
		}
		if bound := c.Web.PasswordAccountID; bound != "" && bound != strings.TrimSpace(c.Owner.ID) {
			return fmt.Errorf("web.password_account_id %q must name the owner %q", bound, c.Owner.ID)
		}
		return nil
	}
	if len(c.Accounts) == 0 {
		return errors.New("accounts must list at least one account")
	}
	// The two shapes are exclusive. A legacy owner beside an accounts list
	// would leave the question of who owns the historical records to
	// whichever reader looked first; migration_owner_id is the one explicit
	// answer to that.
	if strings.TrimSpace(c.Owner.ID) != "" {
		return errors.New("owner.id cannot be set alongside accounts; name the historical owner with migration_owner_id")
	}
	if c.Telegram.OwnerID != 0 {
		return errors.New("telegram.owner_id cannot be set alongside accounts; set telegram_user_id on the account instead")
	}
	ids := map[string]bool{}
	telegramIDs := map[int64]bool{}
	for _, account := range c.Accounts {
		id := strings.TrimSpace(account.ID)
		if !configuredNamePattern.MatchString(id) {
			return fmt.Errorf("account id %q must be 1-64 characters of letters, digits, '.', '_' or '-', starting with a letter or digit", account.ID)
		}
		// IDs name directories under the home, and the home may sit on a
		// case-insensitive filesystem, so two IDs that differ only by case
		// would share one memory directory.
		if ids[strings.ToLower(id)] {
			return fmt.Errorf("duplicate account id %q", id)
		}
		ids[strings.ToLower(id)] = true
		if account.TelegramUserID < 0 {
			return fmt.Errorf("account %q telegram_user_id must be positive when set", id)
		}
		if account.TelegramUserID != 0 {
			if telegramIDs[account.TelegramUserID] {
				return fmt.Errorf("duplicate account telegram_user_id %d", account.TelegramUserID)
			}
			telegramIDs[account.TelegramUserID] = true
		}
	}
	// The environment credentials sign in exactly one account, and the
	// list's order never chooses it: a reordered YAML must not hand the
	// operator's password to someone else.
	bound := c.Web.PasswordAccountID
	if bound == "" {
		return errors.New("web.password_account_id is required when accounts are configured: it names the account EGGY_UI_USER_EMAIL and EGGY_UI_PASSWORD sign in")
	}
	if _, ok := c.Account(bound); !ok {
		return fmt.Errorf("web.password_account_id %q does not name a configured account", bound)
	}
	if owner := strings.TrimSpace(c.MigrationOwnerID); owner != "" {
		if _, ok := c.Account(owner); !ok {
			return fmt.Errorf("migration_owner_id %q does not name a configured account", owner)
		}
	}
	if c.Google.Enabled && strings.TrimSpace(c.Google.ExpectedEmail) == "" {
		return errors.New("google.expected_email is required when accounts are configured and google is enabled: it names Eggy's own Workspace identity, the only one a user may connect")
	}
	return nil
}

// AccountInput is the set of account fields a surface can set. Zero
// TelegramUserID means "no Telegram", both on add and on edit: a blank field
// is the way an owner detaches a sender. There is no credential here: a
// password never passes through internal/config.
type AccountInput struct {
	ID             string
	TelegramUserID int64
}

// AddAccount appends one account. It is refused in legacy mode rather than
// silently converting the deployment: conversion is an explicit choice that
// also has to name a migration owner and the password account.
func AddAccount(path string, input AccountInput) error {
	return AddAccountChecked(path, input, nil)
}

// AddAccountChecked is AddAccount with one more refusal, run under the same
// lock before the append: check may consult the credential store (a retired
// row for the same ID, say) but never mutates config. It exists so the web
// layer can serialize "is this ID free anywhere?" with the YAML write
// without writing YAML itself.
func AddAccountChecked(path string, input AccountInput, check func(id string) error) error {
	return mutate(path, func(cfg *Config) error {
		if !cfg.AccountMode() {
			return errors.New("this deployment uses a single owner; convert it to accounts before adding one")
		}
		id := strings.TrimSpace(input.ID)
		for _, account := range cfg.Accounts {
			if strings.EqualFold(account.ID, id) {
				return fmt.Errorf("account %q already exists", id)
			}
		}
		if check != nil {
			if err := check(id); err != nil {
				return err
			}
		}
		cfg.Accounts = append(cfg.Accounts, AccountConfig{ID: id, TelegramUserID: input.TelegramUserID})
		cfg.normalizeAccounts()
		return nil
	})
}

// EditAccount changes one account's Telegram sender. The ID is the record's
// name in every private table and directory, so it cannot be renamed here.
func EditAccount(path, id string, input AccountInput) error {
	return mutate(path, func(cfg *Config) error {
		index := -1
		for i, account := range cfg.Accounts {
			if account.ID == id {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("account %q is not configured", id)
		}
		cfg.Accounts[index].TelegramUserID = input.TelegramUserID
		return nil
	})
}

// RemoveAccount deletes one account from the list. The last account cannot go:
// a deployment nobody can sign in to is only recoverable from the host. What
// happens to the account's private records and sessions is the caller's job
// (bootstrap revokes sessions on the next resolution); the config only stops
// naming it.
func RemoveAccount(path, id string) error {
	return mutate(path, func(cfg *Config) error {
		kept := make([]AccountConfig, 0, len(cfg.Accounts))
		found := false
		for _, account := range cfg.Accounts {
			if account.ID == id {
				found = true
				continue
			}
			kept = append(kept, account)
		}
		if !found {
			return fmt.Errorf("account %q is not configured", id)
		}
		if len(kept) == 0 {
			return errors.New("the last account cannot be removed")
		}
		if cfg.Web.PasswordAccountID == id {
			return fmt.Errorf("account %q is bound to the environment credentials and cannot be removed; rebind web.password_account_id first", id)
		}
		if cfg.MigrationOwnerID == id {
			// The mapping is a record of what already happened, not a
			// setting; once the account is gone the field would only fail
			// validation.
			cfg.MigrationOwnerID = ""
		}
		cfg.Accounts = kept
		return nil
	})
}

// SetExpectedGoogleEmail writes the address Eggy's shared Google grant must
// belong to. Blank clears it, which validation then refuses while Google is
// enabled in account mode.
func SetExpectedGoogleEmail(path, email string) error {
	normalized := normalizeEmail(email)
	if normalized != "" && !validEmail(normalized) {
		return fmt.Errorf("google.expected_email %q is not an email address", email)
	}
	return mutate(path, func(cfg *Config) error {
		cfg.Google.ExpectedEmail = normalized
		return nil
	})
}

// SetMigrationOwner names the account that receives a legacy deployment's
// private records. It must name a configured account; bootstrap reads it once
// when it finds records without an owner.
func SetMigrationOwner(path, id string) error {
	id = strings.TrimSpace(id)
	return mutate(path, func(cfg *Config) error {
		if _, ok := cfg.Account(id); !ok || !cfg.AccountMode() {
			return fmt.Errorf("migration_owner_id %q does not name a configured account", id)
		}
		cfg.MigrationOwnerID = id
		return nil
	})
}

// RecoveryIdentity is the least a broken config must still establish for
// safe mode to let anyone in: who the accounts are, which one the
// environment credentials sign in, the public origin, and where the session
// database lives. Safe mode uses exactly the normal login, so if this cannot
// be read the repair happens on the host.
type RecoveryIdentity struct {
	AccountMode bool
	Config      Config
	Secrets     Secrets
}

// LoadRecoveryIdentity reads the identity-bearing parts of a config that
// failed to load. It decodes leniently -- unknown keys are exactly the kind
// of mistake that lands a deployment in safe mode -- and validates only the
// account rules, the login binding, and the public base URL.
func LoadRecoveryIdentity(path string, getenv func(string) string) (RecoveryIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RecoveryIdentity{}, fmt.Errorf("open config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RecoveryIdentity{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.normalizeAccounts()
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Dir(path)
	}
	if cfg.Owner.ID == "" && cfg.Telegram.OwnerID != 0 && !cfg.AccountMode() {
		cfg.Owner.ID = strconv.FormatInt(cfg.Telegram.OwnerID, 10)
	}
	identity := RecoveryIdentity{AccountMode: cfg.AccountMode()}
	if err := cfg.validateAccounts(); err != nil {
		return identity, err
	}
	u, err := url.Parse(cfg.Server.PublicBaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return identity, errors.New("server.public_base_url must be an HTTP(S) URL")
	}
	secrets := SecretsFromEnv(getenv)
	if err := cfg.validateLoginSecrets(secrets); err != nil {
		return identity, err
	}
	if strings.TrimSpace(secrets.EncryptionKey) == "" {
		return identity, errors.New("required environment variable EGGY_ENCRYPTION_KEY is missing")
	}
	identity.Config, identity.Secrets = cfg, secrets
	return identity, nil
}

// ConvertInput is everything a legacy deployment needs to become an
// accounts deployment in one write: the people, which account the history
// belongs to, and which one the environment credentials sign in. One write
// rather than several because the intermediate documents would not
// validate.
type ConvertInput struct {
	Accounts          []AccountInput
	MigrationOwnerID  string
	PasswordAccountID string
}

// ConvertToAccounts replaces owner.id and telegram.owner_id with an explicit
// accounts list. The legacy owner's Telegram sender is not carried over by
// itself: the list says who has which sender, and the migration owner is the
// one whose history it becomes. Refused on a deployment that already has
// accounts, where the accounts card edits the list directly.
func ConvertToAccounts(path string, input ConvertInput) error {
	return mutate(path, func(cfg *Config) error {
		if cfg.AccountMode() {
			return errors.New("this deployment already uses accounts")
		}
		cfg.Owner = OwnerConfig{}
		cfg.Telegram = TelegramConfig{}
		cfg.Accounts = nil
		for _, account := range input.Accounts {
			cfg.Accounts = append(cfg.Accounts, AccountConfig{ID: strings.TrimSpace(account.ID), TelegramUserID: account.TelegramUserID})
		}
		cfg.Web.PasswordAccountID = strings.TrimSpace(input.PasswordAccountID)
		cfg.MigrationOwnerID = strings.TrimSpace(input.MigrationOwnerID)
		cfg.normalizeAccounts()
		if cfg.MigrationOwnerID == "" {
			return errors.New("migration_owner_id must name the account that receives the existing history")
		}
		// Heartbeat delivery moved with the sender; a heartbeat that now has
		// nobody on Telegram is refused here, as SetHeartbeat refuses it.
		if cfg.Heartbeat.Interval > 0 && !cfg.TelegramEnabled() {
			return errors.New("heartbeat is configured but no account has a telegram_user_id to deliver it to")
		}
		return nil
	})
}
