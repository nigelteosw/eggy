// Package webui embeds Eggy's built web configuration UI and provides the
// small, Eggy-agnostic primitives its login sits on: signed session tokens
// and login-attempt throttling. It has no knowledge of CommandService, config
// sections, or any other Eggy-specific type.
package webui

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
