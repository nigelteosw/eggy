// Account management: the settings card's routes. Every write goes through
// internal/config's mutations under its lock and validation; this file only
// decides who may do what to whom, and what the card is told.
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// accountView is one row of the People card. PasswordState says how the
// person signs in: "environment" for the account bound to the operator's
// credentials, "set" once a local password exists, "pending" for a
// membership whose credential was never stored. WebLinkAvailable says
// whether /web in their Telegram chat can mint a browser login. Nothing
// here is a hash or a password.
type accountView struct {
	ID               string `json:"id"`
	TelegramUserID   int64  `json:"telegram_user_id,omitempty"`
	DiscordUserID    string `json:"discord_user_id,omitempty"`
	PasswordState    string `json:"password_state"`
	WebLinkAvailable bool   `json:"web_link_available"`
	SignedIn         bool   `json:"signed_in"`
	Self             bool   `json:"self"`
}

const (
	passwordStateEnvironment = "environment"
	passwordStateSet         = "set"
	passwordStatePending     = "pending"
)

type accountsView struct {
	State             string        `json:"state"`
	AccountMode       bool          `json:"account_mode"`
	Accounts          []accountView `json:"accounts"`
	PasswordAccountID string        `json:"password_account_id"`
	// EnvironmentAlias is the username the environment credentials answer
	// to. The password itself never leaves the environment.
	EnvironmentAlias string `json:"environment_alias,omitempty"`
	ExpectedEmail    string `json:"expected_email"`
	MigrationOwnerID string `json:"migration_owner_id,omitempty"`
	// LegacyOwner is the single owner a legacy deployment has, so the
	// conversion form can propose it as the migration owner.
	LegacyOwner      string `json:"legacy_owner,omitempty"`
	LegacyTelegramID int64  `json:"legacy_telegram_id,omitempty"`
	TelegramEnabled  bool   `json:"telegram_enabled"`
	TelegramPairing  bool   `json:"telegram_pairing_available"`
	DiscordEnabled   bool   `json:"discord_enabled"`
	DiscordLinking   bool   `json:"discord_linking_available"`
}

func accountsGetRoute(configPath string, webConfig WebUIConfig, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		self := ""
		if current, ok := sessionFromContext(r.Context()); ok {
			self = current.account.ID
		}
		view := accountsView{
			State: webSuccess, AccountMode: cfg.AccountMode(),
			PasswordAccountID: cfg.PasswordAccountID(), EnvironmentAlias: webConfig.EnvironmentAlias,
			ExpectedEmail: cfg.Google.ExpectedEmail, MigrationOwnerID: cfg.MigrationOwnerID,
			LegacyOwner: cfg.Owner.ID, LegacyTelegramID: cfg.Telegram.OwnerID,
			Accounts:        []accountView{},
			TelegramEnabled: cfg.TelegramEnabled(), TelegramPairing: webConfig.TelegramBotUsername != "" && webConfig.IdentityLinks != nil,
			DiscordEnabled: cfg.DiscordEnabled(), DiscordLinking: webConfig.DiscordLinking && webConfig.IdentityLinks != nil,
		}
		for _, account := range cfg.Principals() {
			row := accountView{ID: account.ID, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID, Self: account.ID == self, PasswordState: passwordStatePending}
			live := false
			if webConfig.Auth != nil {
				record, err := webConfig.Auth.AccountAuth(r.Context(), account.ID)
				switch {
				case errors.Is(err, ports.ErrAuthDenied):
				case err != nil:
					writeWebError(w, http.StatusServiceUnavailable, "credential store is unavailable")
					return
				case !record.Retired:
					live = true
					if record.PasswordHash != "" {
						row.PasswordState = passwordStateSet
					}
				}
			}
			if account.ID == cfg.PasswordAccountID() {
				row.PasswordState = passwordStateEnvironment
			}
			row.WebLinkAvailable = live && cfg.TelegramEnabled() && account.TelegramUserID != 0
			if webConfig.Sessions != nil {
				count, err := webConfig.Sessions.ActiveSessions(r.Context(), account.ID, now())
				if err != nil {
					writeWebError(w, http.StatusServiceUnavailable, "session store is unavailable")
					return
				}
				row.SignedIn = count > 0
			}
			view.Accounts = append(view.Accounts, row)
		}
		writeJSON(w, view)
	}
}

type accountInput struct {
	ID             string `json:"id"`
	TelegramUserID int64  `json:"telegram_user_id"`
	Password       string `json:"password,omitempty"`
}

// accountAddRoute creates a person: membership in YAML, then the credential
// row and initial password in SQLite. The hash is computed before any lock.
// The YAML append is refused for an ID any existing credential row already
// uses, retired included, so a removed person's ID cannot come back with a
// fresh password and their old private records. If the store steps fail
// after the membership is written, the response says so explicitly: the
// account exists, its password is pending, and setting it again is the
// retry.
func accountAddRoute(configPath string, webConfig WebUIConfig, limiter verifyLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input accountInput
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if webConfig.Auth == nil {
			writeWebError(w, http.StatusServiceUnavailable, "credential store is unavailable")
			return
		}
		if err := session.ValidatePassword(input.Password); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !limiter.acquire() {
			w.Header().Set("Retry-After", "1")
			writeWebError(w, http.StatusTooManyRequests, "too many password operations in progress, try again shortly")
			return
		}
		encoded, err := session.HashPassword(input.Password)
		limiter.release()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not store the password")
			return
		}
		id := strings.TrimSpace(input.ID)
		err = config.AddAccountChecked(configPath, config.AccountInput{ID: id, TelegramUserID: input.TelegramUserID}, func(id string) error {
			_, err := webConfig.Auth.AccountAuth(r.Context(), id)
			switch {
			case errors.Is(err, ports.ErrAuthDenied):
				return nil
			case err != nil:
				return err
			}
			return ports.ErrAccountIDUsed
		})
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrAccountIDUsed):
			writeWebError(w, http.StatusBadRequest, "that username was used before and cannot be reissued; choose another")
			return
		default:
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.InitializeAccount != nil {
			if err := webConfig.InitializeAccount(id); err != nil {
				writePendingAccount(w, "account was added but runtime initialization failed; retry setting their password after resolving the error")
				return
			}
		}
		if err := registerAndSetPassword(r.Context(), webConfig, id, encoded); err != nil {
			writePendingAccount(w, "account was added but its password could not be stored; set it again from the People card")
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account added.", Detail: "They can sign in with their username and password now."})
	}
}

// registerAndSetPassword stores the first credential for an account whose
// membership was just written, or for a pending one being retried.
func registerAndSetPassword(ctx context.Context, webConfig WebUIConfig, id, encoded string) error {
	err := webConfig.Auth.RegisterAccountAuth(ctx, id)
	if err != nil && !errors.Is(err, ports.ErrAccountIDUsed) {
		return err
	}
	record, err := webConfig.Auth.AccountAuth(ctx, id)
	if err != nil {
		return err
	}
	if record.Retired {
		return ports.ErrAccountIDUsed
	}
	return webConfig.Auth.SetAccountPassword(ctx, id, encoded, record.Generation)
}

// writePendingAccount is the partial-failure answer: membership exists, the
// credential does not, and the body says which without echoing anything.
func writePendingAccount(w http.ResponseWriter, detail string) {
	body, _ := json.Marshal(struct {
		State          string `json:"state"`
		Title          string `json:"title"`
		Detail         string `json:"detail"`
		AccountCreated bool   `json:"account_created"`
		PasswordState  string `json:"password_state"`
	}{State: webError, Title: "Account is pending.", Detail: detail, AccountCreated: true, PasswordState: passwordStatePending})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(body)
}

func accountEditRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var input accountInput
		if err := decodeAuthBody(r, &input); err != nil || input.Password != "" {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.EditAccount(configPath, id, config.AccountInput{TelegramUserID: input.TelegramUserID}); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account updated.", Detail: "Access changed now."})
	}
}

// accountRemoveRoute takes the account out of config and ends its access
// now: the credential row is retired, sessions and links go, and open
// streams close. Your own account is refused so nobody locks themselves
// out from a form; the last account and the environment-bound one are
// refused by config itself. Once the YAML write has committed the person is
// out at every ingress even if the cleanup below fails; the retired row is
// what keeps the ID from being reused, and a failed cleanup is reconciled
// at the next startup.
func accountRemoveRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if current, ok := sessionFromContext(r.Context()); ok && current.account.ID == id {
			writeWebError(w, http.StatusBadRequest, "you cannot remove your own account; ask another Eggy user to")
			return
		}
		if err := config.RemoveAccount(configPath, id); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.ChatHub != nil {
			webConfig.ChatHub.CloseAccount(id)
		}
		if err := retireAccount(r.Context(), webConfig, id); err != nil {
			writeWebError(w, http.StatusServiceUnavailable, "account was removed from the configuration and is locked out, but its credentials could not be cleaned up; the cleanup is retried at the next restart")
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account removed.", Detail: "They are signed out now. Their private history stays in the database until you delete it, and their username is never reissued."})
	}
}

// retireAccount ends everything issued for a removed account. Retiring the
// credential row also deletes its sessions and links in one transaction;
// the identity links (chat pairings) are a separate table.
func retireAccount(ctx context.Context, webConfig WebUIConfig, id string) error {
	if webConfig.Auth != nil {
		if err := webConfig.Auth.RetireAccountAuth(ctx, id); err != nil {
			return err
		}
	} else if webConfig.Sessions != nil {
		if err := webConfig.Sessions.RevokeAccountSessions(ctx, id); err != nil {
			return err
		}
	}
	if webConfig.IdentityLinks != nil {
		if err := webConfig.IdentityLinks.DeleteIdentityLinks(ctx, id, ""); err != nil {
			return err
		}
	}
	return nil
}

// accountPasswordRoute is POST /api/config/accounts/{id}/password. The
// acting person is whoever the session guard established; the path names
// the target, never the principal. Changing your own password requires the
// current one; resetting another trusted person's does not. The
// environment-bound account has no local password to set: its credential
// is operator configuration. The change advances the generation, so every
// session and link of the target ends, the caller's own included when the
// target is themselves.
func accountPasswordRoute(configPath string, webConfig WebUIConfig, limiter verifyLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, ok := sessionFromContext(r.Context())
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		target := r.PathValue("id")
		var input struct {
			Password        string `json:"password"`
			CurrentPassword string `json:"current_password,omitempty"`
		}
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if webConfig.Auth == nil {
			writeWebError(w, http.StatusServiceUnavailable, "credential store is unavailable")
			return
		}
		if err := session.ValidatePassword(input.Password); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		self := current.account.ID == target
		if self && (input.CurrentPassword == "" || len(input.CurrentPassword) > session.MaxPasswordBytes) {
			writeWebError(w, http.StatusBadRequest, "current password is required to change your own")
			return
		}
		if environmentBound(webConfig, target) {
			writeWebError(w, http.StatusBadRequest, "this account signs in with the deployment environment's credentials; change EGGY_UI_PASSWORD there and restart")
			return
		}
		record, err := webConfig.Auth.AccountAuth(r.Context(), target)
		switch {
		case errors.Is(err, ports.ErrAuthDenied):
			writeWebError(w, http.StatusNotFound, "account not found")
			return
		case err != nil:
			writeWebError(w, http.StatusServiceUnavailable, "credential store is unavailable")
			return
		case record.Retired:
			writeWebError(w, http.StatusNotFound, "account not found")
			return
		}
		if !limiter.acquire() {
			w.Header().Set("Retry-After", "1")
			writeWebError(w, http.StatusTooManyRequests, "too many password operations in progress, try again shortly")
			return
		}
		verified := !self || (record.PasswordHash != "" && session.VerifyPassword(record.PasswordHash, input.CurrentPassword))
		var encoded string
		if verified {
			encoded, err = session.HashPassword(input.Password)
		}
		limiter.release()
		if !verified {
			writeWebError(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not store the password")
			return
		}
		err = withMembership(configPath, webConfig, target, func(m membership) error {
			if m.passwordAccountID == target {
				return errors.New("this account signs in with the deployment environment's credentials")
			}
			return webConfig.Auth.SetAccountPassword(r.Context(), target, encoded, record.Generation)
		})
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrAuthDenied):
			writeWebError(w, http.StatusConflict, "the password changed while this request was running; try again")
			return
		default:
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.ChatHub != nil {
			webConfig.ChatHub.CloseAccount(target)
		}
		if self {
			clearSessionCookie(w)
			writeWebResult(w, webResult{State: webSuccess, Title: "Password changed.", Detail: "You have been signed out everywhere; sign in again with the new password."})
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Password reset.", Detail: "They have been signed out everywhere and any unused sign-in link is void."})
	}
}

// accountRevokeSessionsRoute is POST /api/config/accounts/{id}/revoke-sessions:
// the generation advances without touching the password, so every session
// and link ends. It is the documented manual invalidation for the
// environment-bound account too, whose password cannot be changed here.
func accountRevokeSessionsRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, ok := sessionFromContext(r.Context())
		if !ok {
			writeWebError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		target := r.PathValue("id")
		var input struct{}
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if webConfig.Auth == nil {
			writeWebError(w, http.StatusServiceUnavailable, "credential store is unavailable")
			return
		}
		err := withMembership(configPath, webConfig, target, func(membership) error {
			return webConfig.Auth.RevokeAccountAuth(r.Context(), target)
		})
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrAuthDenied):
			writeWebError(w, http.StatusNotFound, "account not found")
			return
		default:
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.ChatHub != nil {
			webConfig.ChatHub.CloseAccount(target)
		}
		if current.account.ID == target {
			clearSessionCookie(w)
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Sessions revoked.", Detail: "Every session and unused sign-in link of that account has ended."})
	}
}

// telegramEnabledRoute switches Telegram on or off. Enabling requires the
// bot's deployment credentials to already be present in the environment --
// writing enabled:true without them would only be discovered at the next
// restart, in safe mode, so this checks the same two variables
// validateSecrets requires before the write ever happens. Disabling needs no
// such check: there is nothing an absent credential could break by turning
// the channel off.
func telegramEnabledRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	getenv := webConfig.getenv()
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if input.Enabled {
			var missing []string
			if strings.TrimSpace(getenv("TELEGRAM_BOT_TOKEN")) == "" {
				missing = append(missing, "TELEGRAM_BOT_TOKEN")
			}
			if strings.TrimSpace(getenv("TELEGRAM_WEBHOOK_SECRET")) == "" {
				missing = append(missing, "TELEGRAM_WEBHOOK_SECRET")
			}
			if len(missing) > 0 {
				writeWebError(w, http.StatusBadRequest, "set "+strings.Join(missing, " and ")+" in the deployment environment and restart before enabling Telegram")
				return
			}
		}
		if err := config.SetTelegramEnabled(configPath, input.Enabled); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		title := "Telegram enabled."
		if !input.Enabled {
			title = "Telegram disabled."
		}
		writeWebResult(w, webResult{State: webSuccess, Title: title, Detail: restartToApply})
	}
}

// getenv is the configured environment lookup, defaulting to the process
// environment like the raw editor does.
func (c WebUIConfig) getenv() func(string) string {
	if c.Getenv == nil {
		return os.Getenv
	}
	return c.Getenv
}

func expectedEmailSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.SetExpectedGoogleEmail(configPath, input.Email); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Expected Google account saved.", Detail: "Restart Eggy to apply. Only this account can be connected as Eggy."})
	}
}

// accountsConvertRoute turns a single-owner deployment into an accounts
// deployment in one write. It is the one way to get there from the panel;
// once converted, the card edits the list directly. The accounts it names
// get credential rows so /web works for them; their passwords are set
// afterwards from the People card. The environment credentials follow the
// account named as the password account, which must be one of the list.
func accountsConvertRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Accounts          []accountInput `json:"accounts"`
			MigrationOwnerID  string         `json:"migration_owner_id"`
			PasswordAccountID string         `json:"password_account_id"`
		}
		if err := decodeAuthBody(r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		convert := config.ConvertInput{MigrationOwnerID: input.MigrationOwnerID, PasswordAccountID: input.PasswordAccountID}
		for _, account := range input.Accounts {
			if account.Password != "" {
				writeWebError(w, http.StatusBadRequest, "set passwords from the People card after converting")
				return
			}
			convert.Accounts = append(convert.Accounts, config.AccountInput{ID: account.ID, TelegramUserID: account.TelegramUserID})
		}
		if err := config.ConvertToAccounts(configPath, convert); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.Auth != nil {
			for _, account := range convert.Accounts {
				err := webConfig.Auth.RegisterAccountAuth(r.Context(), strings.TrimSpace(account.ID))
				if err != nil && !errors.Is(err, ports.ErrAccountIDUsed) {
					writeWebError(w, http.StatusServiceUnavailable, "converted, but credential rows could not be created; they are created at the next restart")
					return
				}
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Converted to accounts.", Detail: "Restart Eggy. Set each new person's password from the People card; the environment login follows the account you named."})
	}
}
