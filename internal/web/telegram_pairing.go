package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

type TelegramPairingStore interface {
	CreateTelegramPairing(context.Context, string, [32]byte, time.Time) error
	ClaimTelegramPairing(context.Context, [32]byte, time.Time) (string, [16]byte, bool, error)
	FinishTelegramPairing(context.Context, [16]byte, bool) error
	DeleteTelegramPairings(context.Context, string) error
}

func telegramPairingCreateRoute(store TelegramPairingStore, botUsername string, random func([]byte) error, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, ok := sessionFromContext(r.Context())
		if !ok || current.account.ID != r.PathValue("id") {
			writeWebError(w, http.StatusForbidden, "Telegram can only be linked to your own account")
			return
		}
		if store == nil || botUsername == "" {
			writeWebError(w, http.StatusServiceUnavailable, "Telegram pairing is unavailable; check the bot configuration and restart Eggy")
			return
		}
		code := make([]byte, 32)
		if err := random(code); err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not create Telegram pairing")
			return
		}
		expires := now().Add(10 * time.Minute).UTC()
		hash := sha256.Sum256(code)
		if err := store.CreateTelegramPairing(r.Context(), current.account.ID, hash, expires); err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not create Telegram pairing")
			return
		}
		link := &url.URL{Scheme: "https", Host: "t.me", Path: "/" + botUsername}
		query := link.Query()
		query.Set("start", base64.RawURLEncoding.EncodeToString(code))
		link.RawQuery = query.Encode()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			URL       string    `json:"url"`
			ExpiresAt time.Time `json:"expires_at"`
		}{URL: link.String(), ExpiresAt: expires})
	}
}

func telegramUnlinkRoute(configPath string, store TelegramPairingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, ok := sessionFromContext(r.Context())
		if !ok || current.account.ID != r.PathValue("id") {
			writeWebError(w, http.StatusForbidden, "Telegram can only be unlinked from your own account")
			return
		}
		if err := config.UnlinkTelegramAccount(configPath, current.account.ID); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if store != nil {
			if err := store.DeleteTelegramPairings(r.Context(), current.account.ID); err != nil {
				writeWebError(w, http.StatusInternalServerError, "Telegram was unlinked but its pending pairing could not be cleared")
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Telegram unlinked.", Detail: "Messages from the previous sender are refused now."})
	}
}

func cryptoRead(dst []byte) error {
	_, err := io.ReadFull(rand.Reader, dst)
	return err
}
