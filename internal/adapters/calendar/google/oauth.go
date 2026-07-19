package google

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func NewOAuthHandlers(adapter *Adapter, store ports.StateStore, signingKey []byte, now func() time.Time) (http.Handler, http.Handler) {
	start := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := consumeEnrollment(r.Context(), store, r.URL.Query().Get("enrollment"), now()); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		state, err := signOAuthState(signingKey, now())
		if err != nil {
			http.Error(w, "OAuth unavailable", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, adapter.AuthorizationURL(state), http.StatusFound)
	})
	callback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyOAuthState(signingKey, r.URL.Query().Get("state"), now()) {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			return
		}
		auth, err := adapter.ExchangeCode(r.Context(), code)
		if err != nil {
			http.Error(w, "OAuth exchange failed", http.StatusBadGateway)
			return
		}
		state, err := store.Load(r.Context())
		if err != nil {
			http.Error(w, "state unavailable", http.StatusInternalServerError)
			return
		}
		if _, err := store.Update(r.Context(), state.Version, func(state *ports.State) error { state.Calendar = auth; return nil }); err != nil {
			http.Error(w, "state update failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return start, callback
}

func consumeEnrollment(ctx context.Context, store ports.StateStore, token string, now time.Time) error {
	if token == "" {
		return errors.New("missing enrollment token")
	}
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token))
	actual := hex.EncodeToString(sum[:])
	expected := state.Calendar.EnrollmentDigest
	if len(actual) != len(expected) || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 || !now.Before(state.Calendar.EnrollmentExpires) {
		return errors.New("invalid enrollment token")
	}
	_, err = store.Update(ctx, state.Version, func(state *ports.State) error {
		state.Calendar.EnrollmentDigest = ""
		state.Calendar.EnrollmentExpires = time.Time{}
		return nil
	})
	return err
}

func signOAuthState(key []byte, now time.Time) (string, error) {
	if len(key) < 16 {
		return "", errors.New("OAuth signing key is too short")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := strconv.FormatInt(now.Unix(), 10) + ":" + base64.RawURLEncoding.EncodeToString(nonce)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyOAuthState(key []byte, state string, now time.Time) bool {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	signature, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	timestamp, err := strconv.ParseInt(strings.SplitN(string(payload), ":", 2)[0], 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(timestamp, 0))
	return age >= 0 && age <= 10*time.Minute
}
