package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// NewToken returns a fresh opaque credential and the hash under which it is
// stored. The raw value goes to the browser and nowhere else; the store only
// ever sees the hash, so a database read cannot be replayed as a cookie.
//
// 32 random bytes, URL-safe base64 without padding: long enough that
// guessing is not a strategy, and a character set that survives a cookie
// header and a query string unchanged.
func NewToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken is the one function that turns a presented token into a store
// key. SHA-256 is enough: the input is 256 bits of entropy, so there is
// nothing for a slower hash to protect against.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Equal compares two tokens in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
