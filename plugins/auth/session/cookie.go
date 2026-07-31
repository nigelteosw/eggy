// Package session holds the primitives the owner's login sits on: signed
// session tokens and login-attempt throttling. It answers "who may talk to
// Eggy", which is the opposite direction of trust from the OAuth grants in
// plugins/tools/* -- those answer "what may Eggy do on the owner's behalf".
// The two are kept apart deliberately; merging them produces one security
// object that owns everything and is understood by no one.
//
// Nothing here knows about CommandService, config sections, or any other
// Eggy-specific type. It lives beside plugins/auth/authfile rather than in the
// package that serves the JavaScript bundle, because session crypto is not an
// asset.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// SignSession returns an HMAC-SHA256-signed session token encoding
// expiresAt, verifiable later with only key -- no server-side session store.
// The token carries no other payload: Eggy's web UI has exactly one owner
// account, so there is nothing else to encode.
func SignSession(key []byte, expiresAt time.Time) string {
	payload := strconv.FormatInt(expiresAt.Unix(), 10)
	return payload + "." + hex.EncodeToString(sign(key, payload))
}

// VerifySession reports whether token was produced by SignSession with key
// and has not yet expired as of now.
func VerifySession(key []byte, token string, now time.Time) bool {
	payload, sigHex, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, sign(key, payload)) {
		return false
	}
	expiresAtUnix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(expiresAtUnix, 0))
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}
