package ports

import (
	"context"
	"errors"
	"time"
)

// ErrMarketplaceInstallNotFound is returned for an install ID with no record.
var ErrMarketplaceInstallNotFound = errors.New("install not found")

// ErrMarketplaceInstallExists is returned when an item from one source is
// already installed, so a second install cannot overwrite the first's record.
var ErrMarketplaceInstallExists = errors.New("already installed")

// MarketplaceFile is one thing an install wrote, with the content hash it had
// when written. Comparing that hash with the current content is how an update
// finds the owner's local edits.
type MarketplaceFile struct {
	Kind        string // "skill" or "mcp_server"
	Target      string // skill name, or MCP server name
	ContentHash string
}

// MarketplaceInstall is the durable record of one installed plugin or skill
// from a marketplace source. It lets the owner update, disable, or remove what
// one install brought in as a single unit.
type MarketplaceInstall struct {
	ID          string
	SourceName  string
	Repo        string
	Item        string
	CommitSHA   string
	PlanDigest  string
	InstalledBy string
	InstalledAt time.Time
	UpdatedAt   time.Time
	Disabled    bool
	Files       []MarketplaceFile
}

// MarketplaceStore persists install records. Each write is one transaction, so
// a record and its file list change together or not at all.
type MarketplaceStore interface {
	CreateInstall(context.Context, MarketplaceInstall) error
	ReplaceInstall(context.Context, MarketplaceInstall) error
	ListInstalls(context.Context) ([]MarketplaceInstall, error)
	GetInstall(context.Context, string) (MarketplaceInstall, error)
	SetInstallDisabled(context.Context, string, bool) error
	DeleteInstall(context.Context, string) error
}
