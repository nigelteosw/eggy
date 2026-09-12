// Accounts: who may use this deployment. Two shapes describe the same thing.
// The legacy shape is one owner.id (or a telegram.owner_id it is derived
// from); the explicit shape is an accounts list, each entry naming the Google
// address that may enroll it and, optionally, the Telegram sender that speaks
// for it. Both normalize to Principals, so the runtime only ever sees a list
// of accounts and never asks which shape it came from.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AccountConfig is one person. There is no role field, and none is coming:
// every account holds every capability, and what separates accounts is
// ownership of private records, not permission.
type AccountConfig struct {
	ID string `yaml:"id"`
	// GoogleEmail is the address that may enroll this account through Google
	// Sign-In. It is compared exactly after trimming and lowercasing; dots and
	// plus-suffixes are not collapsed, because Google's own subject binding is
	// what identifies the person after the first login, not the address.
	GoogleEmail string `yaml:"google_email"`
	// TelegramUserID is the numeric sender that maps onto this account. Zero
	// means this person does not use Telegram. Never a username: those are
	// reassignable.
	TelegramUserID int64 `yaml:"telegram_user_id,omitempty"`
}

// WebConfig holds the inbound login settings. It is separate from
// GoogleConfig because the two Google OAuth clients are different things: this
// one is a Web application client that signs people in, that one is a Desktop
// client that authorizes Eggy's own shared Workspace grant.
type WebConfig struct {
	GoogleLogin GoogleLoginConfig `yaml:"google_login,omitempty"`
}

// GoogleLoginConfig is the Web application OAuth client Google Sign-In uses.
// The client ID travels in the authorization URL so it lives in YAML; the
// secret is named by environment variable like every other credential.
type GoogleLoginConfig struct {
	ClientID        string `yaml:"client_id,omitempty"`
	ClientSecretEnv string `yaml:"client_secret_env,omitempty"`
}

// Configured reports whether an inbound login client is set at all. Half
// set is an error Validate reports, not a configured client.
func (g GoogleLoginConfig) Configured() bool {
	return strings.TrimSpace(g.ClientID) != "" || strings.TrimSpace(g.ClientSecretEnv) != ""
}

// AccountMode reports whether the deployment authenticates people through the
// explicit accounts list. Anything that only makes sense with that list --
// the login client, the migration mapping -- also selects it, so a config
// that names one and forgets the list is reported as missing the list rather
// than silently treated as a legacy owner.
func (c Config) AccountMode() bool {
	return len(c.Accounts) > 0 || c.Web.GoogleLogin.Configured() || strings.TrimSpace(c.MigrationOwnerID) != ""
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

// AccountForEmail resolves the account a normalized Google address may
// enroll, exactly as written after trimming and lowercasing.
func (c Config) AccountForEmail(email string) (AccountConfig, bool) {
	normalized := normalizeEmail(email)
	if normalized == "" {
		return AccountConfig{}, false
	}
	for _, account := range c.Principals() {
		if account.GoogleEmail == normalized {
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
		c.Accounts[i].GoogleEmail = normalizeEmail(c.Accounts[i].GoogleEmail)
	}
	c.Google.ExpectedEmail = normalizeEmail(c.Google.ExpectedEmail)
	c.MigrationOwnerID = strings.TrimSpace(c.MigrationOwnerID)
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
	emails := map[string]bool{}
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
		email := normalizeEmail(account.GoogleEmail)
		if !validEmail(email) {
			return fmt.Errorf("account %q google_email %q is not an email address", id, account.GoogleEmail)
		}
		if emails[email] {
			return fmt.Errorf("duplicate account google_email %q", email)
		}
		emails[email] = true
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
	login := c.Web.GoogleLogin
	if strings.TrimSpace(login.ClientID) == "" {
		return errors.New("web.google_login.client_id is required when accounts are configured")
	}
	if strings.TrimSpace(login.ClientSecretEnv) == "" {
		return errors.New("web.google_login.client_secret_env is required when accounts are configured")
	}
	if !environmentNamePattern.MatchString(login.ClientSecretEnv) {
		return fmt.Errorf("web.google_login.client_secret_env %q is not a valid environment variable name", login.ClientSecretEnv)
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
// is the way an owner detaches a sender.
type AccountInput struct {
	ID             string
	GoogleEmail    string
	TelegramUserID int64
}

// AddAccount appends one account. It is refused in legacy mode rather than
// silently converting the deployment: conversion is an explicit choice that
// also has to name a migration owner and a login client.
func AddAccount(path string, input AccountInput) error {
	return mutate(path, func(cfg *Config) error {
		if !cfg.AccountMode() {
			return errors.New("this deployment uses a single owner; convert it to accounts before adding one")
		}
		id := strings.TrimSpace(input.ID)
		if _, exists := cfg.Account(id); exists {
			return fmt.Errorf("account %q already exists", id)
		}
		cfg.Accounts = append(cfg.Accounts, AccountConfig{ID: id, GoogleEmail: normalizeEmail(input.GoogleEmail), TelegramUserID: input.TelegramUserID})
		cfg.normalizeAccounts()
		return nil
	})
}

// EditAccount changes one account's address and Telegram sender. The ID is
// the record's name in every private table and directory, so it cannot be
// renamed here. bound says whether the account has already enrolled a Google
// identity: while it has, its address is pinned, because the binding is what
// identifies the person and a changed address must never quietly hand the
// account to someone else. Reset the binding first, then edit.
func EditAccount(path, id string, input AccountInput, bound bool) error {
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
		email := normalizeEmail(input.GoogleEmail)
		if bound && email != cfg.Accounts[index].GoogleEmail {
			return fmt.Errorf("account %q has enrolled with %s; reset its binding before changing the address", id, cfg.Accounts[index].GoogleEmail)
		}
		cfg.Accounts[index].GoogleEmail = email
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

// SetGoogleLogin writes the inbound Web application client. Validation
// refuses a half-set pair, so both arrive together.
func SetGoogleLogin(path, clientID, clientSecretEnv string) error {
	clientID, clientSecretEnv = strings.TrimSpace(clientID), strings.TrimSpace(clientSecretEnv)
	if clientID == "" {
		return errors.New("web.google_login.client_id is required")
	}
	if !environmentNamePattern.MatchString(clientSecretEnv) {
		return fmt.Errorf("web.google_login.client_secret_env %q is not a valid environment variable name", clientSecretEnv)
	}
	return mutate(path, func(cfg *Config) error {
		cfg.Web.GoogleLogin = GoogleLoginConfig{ClientID: clientID, ClientSecretEnv: clientSecretEnv}
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
// safe mode to let anyone in: who the accounts are, the login client, the
// public origin the callback hangs off, and where the session database
// lives. Safe mode never falls back to a password in account mode, so if
// this cannot be read the repair happens on the host.
type RecoveryIdentity struct {
	AccountMode bool
	Config      Config
	Secrets     Secrets
}

// LoadRecoveryIdentity reads the identity-bearing parts of a config that
// failed to load. It decodes leniently -- unknown keys are exactly the kind
// of mistake that lands a deployment in safe mode -- and validates only the
// account rules and the public base URL. A document with no accounts is the
// legacy shape and reports AccountMode false with nothing else checked.
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
		cfg.DataDir = "/data"
	}
	if !cfg.AccountMode() {
		return RecoveryIdentity{AccountMode: false}, nil
	}
	if err := cfg.validateAccounts(); err != nil {
		return RecoveryIdentity{AccountMode: true}, err
	}
	u, err := url.Parse(cfg.Server.PublicBaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return RecoveryIdentity{AccountMode: true}, errors.New("server.public_base_url must be an HTTP(S) URL")
	}
	secrets := SecretsFromEnv(getenv)
	secrets.GoogleLoginClientSecret = getenv(cfg.Web.GoogleLogin.ClientSecretEnv)
	if strings.TrimSpace(secrets.GoogleLoginClientSecret) == "" {
		return RecoveryIdentity{AccountMode: true}, fmt.Errorf("required environment variable %s is missing", cfg.Web.GoogleLogin.ClientSecretEnv)
	}
	if strings.TrimSpace(secrets.EncryptionKey) == "" {
		return RecoveryIdentity{AccountMode: true}, errors.New("required environment variable EGGY_ENCRYPTION_KEY is missing")
	}
	return RecoveryIdentity{AccountMode: true, Config: cfg, Secrets: secrets}, nil
}

// ConvertInput is everything a legacy deployment needs to become an
// accounts deployment in one write: the people, the login client, and which
// account the history belongs to. One write rather than several because the
// intermediate documents would not validate.
type ConvertInput struct {
	Accounts             []AccountInput
	LoginClientID        string
	LoginClientSecretEnv string
	MigrationOwnerID     string
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
			cfg.Accounts = append(cfg.Accounts, AccountConfig{ID: strings.TrimSpace(account.ID), GoogleEmail: normalizeEmail(account.GoogleEmail), TelegramUserID: account.TelegramUserID})
		}
		cfg.Web.GoogleLogin = GoogleLoginConfig{ClientID: strings.TrimSpace(input.LoginClientID), ClientSecretEnv: strings.TrimSpace(input.LoginClientSecretEnv)}
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
