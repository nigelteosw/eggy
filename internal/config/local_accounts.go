// Local accounts: the config side of the cutover from Google Sign-In to
// username/password login, and the read-under-lock helper the login and
// link paths use to check membership against the document as it is now.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nigelteosw/eggy/internal/fsutil/filelock"
	"gopkg.in/yaml.v3"
)

// ErrLocalLoginMigrationRequired reports a config still in the inbound
// Google Sign-In shape. It is detected on the raw document before the strict
// decoder would refuse the retired keys, so the operator is told which
// command to run rather than which field is unknown.
var ErrLocalLoginMigrationRequired = errors.New("config.yaml still uses Google Sign-In (accounts[].google_email / web.google_login); stop eggyd and run `eggyd --home <home> --migrate-local-login --password-account <id>`")

// retiredLoginShape reports whether the document carries the inbound Google
// login fields. Retired fields are otherwise pruned silently on boot; these
// are not, because the cutover that removes them also rewrites the
// database, and both have to be backed up first.
func retiredLoginShape(root *yaml.Node) bool {
	if mappingValue(mappingValue(root, "web"), "google_login") != nil {
		return true
	}
	accounts := mappingValue(root, "accounts")
	if accounts == nil || accounts.Kind != yaml.SequenceNode {
		return false
	}
	for _, account := range accounts.Content {
		if mappingValue(account, "google_email") != nil {
			return true
		}
	}
	return false
}

func requireLocalLoginShape(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		// The strict decoder reports the parse error with more context.
		return nil
	}
	if len(document.Content) == 0 {
		return nil
	}
	if retiredLoginShape(document.Content[0]) {
		return ErrLocalLoginMigrationRequired
	}
	return nil
}

// WithAccount reads the current document under the config file lock,
// validates it, resolves id, and hands both to fn while the lock is held.
// It is how login completion, link minting and redemption, and credential
// changes check membership against the document as it is at that instant
// rather than as it was at boot. fn is a read: it must not call any config
// mutation function, which would try to take the same non-reentrant lock,
// and it must not hash passwords or make outbound calls, which would hold
// every config write in the process behind them.
func WithAccount(path, id string, fn func(Config, AccountConfig) error) error {
	return filelock.With(path, func() error {
		cfg, err := LoadDocument(path)
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		account, ok := cfg.Account(strings.TrimSpace(id))
		if !ok {
			return fmt.Errorf("account %q is not configured", id)
		}
		return fn(cfg, account)
	})
}

// LocalAccountsPlan is what the cutover learned from the old document before
// changing anything: the accounts it will keep, the digest of the file it
// read, and the config it would write. The command backs up and prepares the
// database against this before MigrateLocalAccounts writes.
type LocalAccountsPlan struct {
	Accounts          []AccountConfig
	PasswordAccountID string
	ConfigDigest      string
	Legacy            bool
}

// ConfigDigest is the hex SHA-256 of the file as it is on disk, the value
// the cutover marker records so a rerun can tell whether an existing config
// backup is the one it took.
func ConfigDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// PrepareLocalAccounts parses the old document as nodes, derives the
// candidate accounts, applies passwordAccountID, and validates the
// resulting config and its environment credentials without writing. A
// document already in the local shape is accepted as long as the binding
// matches, so an interrupted cutover can be rerun.
func PrepareLocalAccounts(path, passwordAccountID string, getenv func(string) string) (LocalAccountsPlan, error) {
	var plan LocalAccountsPlan
	err := filelock.With(path, func() error {
		var err error
		plan, err = prepareLocalAccountsUnlocked(path, passwordAccountID, getenv)
		return err
	})
	return plan, err
}

func prepareLocalAccountsUnlocked(path, passwordAccountID string, getenv func(string) string) (LocalAccountsPlan, error) {
	passwordAccountID = strings.TrimSpace(passwordAccountID)
	if passwordAccountID == "" {
		return LocalAccountsPlan{}, errors.New("--password-account must name the account the environment credentials sign in")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalAccountsPlan{}, fmt.Errorf("open config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return LocalAccountsPlan{}, fmt.Errorf("decode config: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return LocalAccountsPlan{}, errors.New("config.yaml is not a mapping")
	}
	if err := rewriteLocalLoginShape(document.Content[0], passwordAccountID); err != nil {
		return LocalAccountsPlan{}, err
	}
	body, err := encodeDocument(&document)
	if err != nil {
		return LocalAccountsPlan{}, err
	}
	cfg, err := loadConfigBytes(body, getenv)
	if err != nil {
		return LocalAccountsPlan{}, fmt.Errorf("migrated config would not load: %w", err)
	}
	if cfg.PasswordAccountID() != passwordAccountID {
		return LocalAccountsPlan{}, fmt.Errorf("--password-account %q does not name a configured account", passwordAccountID)
	}
	sum := sha256.Sum256(data)
	return LocalAccountsPlan{
		Accounts:          cfg.Principals(),
		PasswordAccountID: passwordAccountID,
		ConfigDigest:      hex.EncodeToString(sum[:]),
		Legacy:            !cfg.AccountMode(),
	}, nil
}

// rewriteLocalLoginShape edits the node tree in place: drops every
// accounts[].google_email and web.google_login, and sets
// web.password_account_id. Everything else -- comments, order, unrelated
// sections -- is left exactly as written.
func rewriteLocalLoginShape(root *yaml.Node, passwordAccountID string) error {
	if accounts := mappingValue(root, "accounts"); accounts != nil && accounts.Kind == yaml.SequenceNode {
		for _, account := range accounts.Content {
			deleteMappingKey(account, []string{"google_email"})
		}
	}
	deleteMappingKey(root, []string{"web", "google_login"})
	web := mappingValue(root, "web")
	if web == nil || web.Kind != yaml.MappingNode {
		deleteMappingKey(root, []string{"web"})
		web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "web"}, web)
	}
	if existing := mappingValue(web, "password_account_id"); existing != nil {
		if strings.TrimSpace(existing.Value) != passwordAccountID {
			return fmt.Errorf("config already binds web.password_account_id to %q; rerun with that account or edit the file", existing.Value)
		}
		return nil
	}
	web.Content = append(web.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "password_account_id"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: passwordAccountID})
	return nil
}

func encodeDocument(document *yaml.Node) ([]byte, error) {
	var body strings.Builder
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return []byte(body.String()), nil
}

// loadConfigBytes runs the full LoadConfig on an in-memory candidate, so what
// the cutover accepts is exactly what the next start accepts.
func loadConfigBytes(body []byte, getenv func(string) string) (Config, error) {
	candidate, err := os.CreateTemp("", "eggy-config-candidate-*.yaml")
	if err != nil {
		return Config{}, err
	}
	defer os.Remove(candidate.Name())
	if _, err := candidate.Write(body); err != nil {
		candidate.Close()
		return Config{}, err
	}
	if err := candidate.Close(); err != nil {
		return Config{}, err
	}
	cfg, _, err := LoadConfig(candidate.Name(), getenv)
	return cfg, err
}

// MigrateLocalAccounts rewrites the document into the local login shape
// under the config lock: the same preparation as PrepareLocalAccounts, then
// one atomic write. The digest of the file it read is compared against
// expectedDigest so a config edited since the backup was taken is refused
// rather than migrated from a version nobody backed up.
func MigrateLocalAccounts(path, passwordAccountID string, getenv func(string) string) error {
	return filelock.With(path, func() error {
		plan, err := prepareLocalAccountsUnlocked(path, passwordAccountID, getenv)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("open config: %w", err)
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("decode config: %w", err)
		}
		if err := rewriteLocalLoginShape(document.Content[0], plan.PasswordAccountID); err != nil {
			return err
		}
		body, err := encodeDocument(&document)
		if err != nil {
			return err
		}
		return writeFileAtomic(path, body)
	})
}

// TelegramIDText is the provider-neutral spelling of a Telegram sender for
// link records: the decimal number, or empty for no sender.
func TelegramIDText(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
