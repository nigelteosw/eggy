package ports

import (
	"context"
	"errors"
	"time"
)

// ErrAuthDenied is the one answer for every credential failure: wrong
// password, unknown account, retired account, stale generation, consumed or
// expired link. A caller cannot tell them apart, and neither can whoever is
// guessing.
var ErrAuthDenied = errors.New("authentication denied")

// ErrAccountIDUsed reports an account ID with an auth row already behind it,
// pending or retired included. A removed person's ID is never reissued: the
// retired row is what keeps a new "partner" from inheriting the old
// partner's private records.
var ErrAccountIDUsed = errors.New("account id has already been used")

// AccountAuth is the machine-managed credential and lifecycle record for one
// account. Membership itself stays in YAML; this row says how the member may
// authenticate and which generation of credentials is current.
type AccountAuth struct {
	AccountID string
	// PasswordHash is empty for a pending account (membership written,
	// password never set) and for the environment-backed account, whose
	// password is operator configuration rather than a stored record.
	PasswordHash string
	// Generation increments on every reset or revocation. Sessions and links
	// are minted against a generation and become invalid when it moves on.
	Generation int64
	Retired    bool
}

// WebLoginLink is the metadata of one single-use browser login link minted
// from a verified chat sender. The raw token is never stored; the store keys
// the row by its hash.
type WebLoginLink struct {
	AccountID string
	// SenderID is the provider-neutral text of the verified sender that
	// asked for the link. Redemption requires the same mapping to still hold.
	SenderID   string
	Generation int64
	ExpiresAt  time.Time
}

// AccountAuthStore persists credentials, generations, and single-use login
// links. Every method that creates a session is conditional on the
// generation the caller verified: checking a password and then issuing a
// session unconditionally would let a password that was reset in between
// sign in once more.
type AccountAuthStore interface {
	// RegisterAccountAuth inserts the row for a new account at generation 1
	// exactly once; any existing row, pending or retired, is ErrAccountIDUsed.
	RegisterAccountAuth(ctx context.Context, accountID string) error
	AccountAuth(ctx context.Context, accountID string) (AccountAuth, error)
	// SetAccountPassword replaces the hash, increments the generation, and
	// deletes the account's sessions and links atomically, provided the row
	// is still at expectedGeneration and not retired.
	SetAccountPassword(ctx context.Context, accountID, encoded string, expectedGeneration int64) error
	// RetireAccountAuth clears the hash, marks the row retired, and revokes
	// everything issued for it. A retired row is never reactivated.
	RetireAccountAuth(ctx context.Context, accountID string) error
	// RevokeAccountAuth increments the generation and revokes sessions and
	// links without touching the password.
	RevokeAccountAuth(ctx context.Context, accountID string) error
	// CreateAuthenticatedSession issues a session only while the account is
	// still at generation and not retired.
	CreateAuthenticatedSession(ctx context.Context, hash, accountID string, generation int64, expiresAt time.Time) error
	// CreateWebLoginLink stores a link, replacing any outstanding one for the
	// account, only while the account is still at link.Generation.
	CreateWebLoginLink(ctx context.Context, hash string, link WebLoginLink, now time.Time) error
	// WebLoginLink returns an unexpired link's metadata without consuming it.
	WebLoginLink(ctx context.Context, hash string, now time.Time) (WebLoginLink, error)
	// RedeemWebLoginLink consumes exactly the link matching expected and
	// creates the session in one transaction; either both happen or neither.
	RedeemWebLoginLink(ctx context.Context, linkHash, sessionHash string, expected WebLoginLink, now, sessionExpiry time.Time) error
}
