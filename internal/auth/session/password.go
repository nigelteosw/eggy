package session

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

// Local passwords are stored as one fixed, versioned encoding:
//
//	eggy-pbkdf2-sha256-v1$600000$<raw-base64-salt>$<raw-base64-key>
//
// PBKDF2-HMAC-SHA256 is chosen because it ships in the standard library, not
// because it is memory-hard. The parameters are part of the version rather
// than read back from the stored string: a stored row never gets to choose
// its own work factor, so a tampered or malformed encoding can neither make
// verification cheap nor make it unbounded.
const (
	passwordVersion    = "eggy-pbkdf2-sha256-v1"
	passwordIterations = 600000
	passwordIterField  = "600000"
	passwordSaltBytes  = 16
	passwordKeyBytes   = 32

	// MinPasswordBytes is the floor for passwords people choose. Environment
	// passwords configured by the operator are exempt (see
	// HashEnvironmentPassword) so an existing deployment is not locked out.
	MinPasswordBytes = 12
	// MaxPasswordBytes bounds every password Eggy accepts on any path.
	MaxPasswordBytes = 256
)

var (
	passwordEncoding = base64.RawStdEncoding.Strict()
	encodedSaltLen   = passwordEncoding.EncodedLen(passwordSaltBytes)
	encodedKeyLen    = passwordEncoding.EncodedLen(passwordKeyBytes)
	encodedTotalLen  = len(passwordVersion) + 1 + len(passwordIterField) + 1 + encodedSaltLen + 1 + encodedKeyLen
)

// ValidatePassword reports whether password may become a new local
// password: 12–256 bytes of valid UTF-8. Nothing is trimmed or normalized;
// what the person typed is what they must type again.
func ValidatePassword(password string) error {
	if err := boundPassword(password); err != nil {
		return err
	}
	if len(password) < MinPasswordBytes {
		return errors.New("password must be at least 12 bytes")
	}
	return nil
}

func boundPassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if len(password) > MaxPasswordBytes {
		return errors.New("password must be at most 256 bytes")
	}
	if !utf8.ValidString(password) {
		return errors.New("password must be valid UTF-8")
	}
	return nil
}

// HashPassword validates and encodes a person-chosen password.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return encodePassword(password)
}

// HashEnvironmentPassword encodes the operator-configured environment
// password into the same verification format so a wrong guess against the
// environment account costs the same work as one against a local account.
// It keeps the configured value exactly and only enforces the shared upper
// bound; the result is held in memory and never persisted.
func HashEnvironmentPassword(password string) (string, error) {
	if err := boundPassword(password); err != nil {
		return "", err
	}
	return encodePassword(password)
}

func encodePassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return "", err
	}
	return passwordVersion + "$" + passwordIterField + "$" +
		passwordEncoding.EncodeToString(salt) + "$" +
		passwordEncoding.EncodeToString(key), nil
}

// VerifyPassword reports whether password matches the stored encoding. Every
// structural check happens before any key derivation, and a malformed
// encoding is simply false: callers that need a constant-cost failure verify
// a known-good dummy hash instead.
func VerifyPassword(encoded, password string) bool {
	if boundPassword(password) != nil {
		return false
	}
	if len(encoded) != encodedTotalLen {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordVersion || parts[1] != passwordIterField {
		return false
	}
	if len(parts[2]) != encodedSaltLen || len(parts[3]) != encodedKeyLen {
		return false
	}
	salt, err := passwordEncoding.DecodeString(parts[2])
	if err != nil || len(salt) != passwordSaltBytes {
		return false
	}
	want, err := passwordEncoding.DecodeString(parts[3])
	if err != nil || len(want) != passwordKeyBytes {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
