// Identity linking: the panel mints a single-use token for the signed-in
// person, they redeem it from a chat surface, and the redemption binds that
// surface's verified sender to their account. One store and one flow serve
// every connection; each connection only differs in how the token is handed
// over (a Telegram deep link, a Discord DM command) and which config field
// the binding lands in.
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

// TelegramConnection and DiscordConnection are the connection IDs tokens
// are minted under; the redeeming adapter claims under the same one.
const (
	TelegramConnection = "telegram"
	DiscordConnection  = config.DiscordConnection
)

type IdentityLinkStore interface {
	CreateIdentityLink(ctx context.Context, accountID, connection string, hash [32]byte, expires time.Time) error
	ClaimIdentityLink(ctx context.Context, connection string, hash [32]byte, now time.Time) (string, [16]byte, bool, error)
	FinishIdentityLink(ctx context.Context, claim [16]byte, success bool) error
	DeleteIdentityLinks(ctx context.Context, accountID, connection string) error
}

const identityLinkTTL = 10 * time.Minute

// mintIdentityLink creates a token for the signed-in person on one
// connection and returns its wire form. Only the hash is stored. The person
// must be linking their own account: nobody links on someone else's behalf.
func mintIdentityLink(w http.ResponseWriter, r *http.Request, store IdentityLinkStore, connection string, random func([]byte) error, now func() time.Time) (token string, expires time.Time, ok bool) {
	current, found := sessionFromContext(r.Context())
	if !found || current.account.ID != r.PathValue("id") {
		writeWebError(w, http.StatusForbidden, connection+" can only be linked to your own account")
		return "", time.Time{}, false
	}
	code := make([]byte, 32)
	if err := random(code); err != nil {
		writeWebError(w, http.StatusInternalServerError, "could not create "+connection+" link")
		return "", time.Time{}, false
	}
	expires = now().Add(identityLinkTTL).UTC()
	hash := sha256.Sum256(code)
	if err := store.CreateIdentityLink(r.Context(), current.account.ID, connection, hash, expires); err != nil {
		writeWebError(w, http.StatusInternalServerError, "could not create "+connection+" link")
		return "", time.Time{}, false
	}
	return base64.RawURLEncoding.EncodeToString(code), expires, true
}

func telegramPairingCreateRoute(store IdentityLinkStore, botUsername string, random func([]byte) error, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || botUsername == "" {
			writeWebError(w, http.StatusServiceUnavailable, "Telegram pairing is unavailable; check the bot configuration and restart Eggy")
			return
		}
		token, expires, ok := mintIdentityLink(w, r, store, TelegramConnection, random, now)
		if !ok {
			return
		}
		link := &url.URL{Scheme: "https", Host: "t.me", Path: "/" + botUsername}
		query := link.Query()
		query.Set("start", token)
		link.RawQuery = query.Encode()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			URL       string    `json:"url"`
			ExpiresAt time.Time `json:"expires_at"`
		}{URL: link.String(), ExpiresAt: expires})
	}
}

func telegramUnlinkRoute(configPath string, store IdentityLinkStore) http.HandlerFunc {
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
			if err := store.DeleteIdentityLinks(r.Context(), current.account.ID, TelegramConnection); err != nil {
				writeWebError(w, http.StatusInternalServerError, "Telegram was unlinked but its pending pairing could not be cleared")
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Telegram unlinked.", Detail: "Messages from the previous sender are refused now."})
	}
}

// discordLinkCreateRoute mints a token the person sends to the bot in a
// private DM as "/link <token>". Discord has no deep link that carries a
// payload into a DM, so the token itself is shown; the DM URL is only a
// shortcut to the right conversation and needs the application ID.
func discordLinkCreateRoute(store IdentityLinkStore, applicationID string, random func([]byte) error, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeWebError(w, http.StatusServiceUnavailable, "Discord linking is unavailable; enable Discord and restart Eggy")
			return
		}
		token, expires, ok := mintIdentityLink(w, r, store, DiscordConnection, random, now)
		if !ok {
			return
		}
		dm := ""
		if applicationID != "" {
			dm = "https://discord.com/users/" + url.PathEscape(applicationID)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			Command   string    `json:"command"`
			DMURL     string    `json:"dm_url,omitempty"`
			ExpiresAt time.Time `json:"expires_at"`
		}{Command: "/link " + token, DMURL: dm, ExpiresAt: expires})
	}
}

func discordUnlinkRoute(configPath string, store IdentityLinkStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, ok := sessionFromContext(r.Context())
		if !ok || current.account.ID != r.PathValue("id") {
			writeWebError(w, http.StatusForbidden, "Discord can only be unlinked from your own account")
			return
		}
		if err := config.UnlinkDiscordAccount(configPath, current.account.ID); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if store != nil {
			if err := store.DeleteIdentityLinks(r.Context(), current.account.ID, DiscordConnection); err != nil {
				writeWebError(w, http.StatusInternalServerError, "Discord was unlinked but its pending token could not be cleared")
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Discord unlinked.", Detail: "DMs from the previous user are refused now."})
	}
}

func cryptoRead(dst []byte) error {
	_, err := io.ReadFull(rand.Reader, dst)
	return err
}
