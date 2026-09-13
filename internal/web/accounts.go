// Account management: the settings card's routes. Every write goes through
// internal/config's mutations under its lock and validation; this file only
// decides who may do what to whom, and what the card is told.
package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
)

// accountView is one row of the accounts card. Enrolled says whether a
// Google identity is bound; SignedIn whether any session is live. Neither
// carries the identity itself: the subject is a provider detail nobody in
// the panel needs.
type accountView struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	TelegramUserID int64  `json:"telegram_user_id,omitempty"`
	Enrolled       bool   `json:"enrolled"`
	SignedIn       bool   `json:"signed_in"`
	Self           bool   `json:"self"`
}

type accountsView struct {
	State         string        `json:"state"`
	AccountMode   bool          `json:"account_mode"`
	Accounts      []accountView `json:"accounts"`
	LoginClientID string        `json:"login_client_id"`
	// LoginClientSecretEnv is the variable's name. Its value never leaves
	// the environment.
	LoginClientSecretEnv string `json:"login_client_secret_env"`
	ExpectedEmail        string `json:"expected_email"`
	MigrationOwnerID     string `json:"migration_owner_id,omitempty"`
	// LegacyOwner is the single owner a legacy deployment has, so the
	// conversion form can propose it as the migration owner.
	LegacyOwner         string `json:"legacy_owner,omitempty"`
	LegacyTelegramID    int64  `json:"legacy_telegram_id,omitempty"`
	LegacyTelegramEmail string `json:"-"`
	TelegramEnabled     bool   `json:"telegram_enabled"`
	TelegramPairing     bool   `json:"telegram_pairing_available"`
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
			LoginClientID: cfg.Web.GoogleLogin.ClientID, LoginClientSecretEnv: cfg.Web.GoogleLogin.ClientSecretEnv,
			ExpectedEmail: cfg.Google.ExpectedEmail, MigrationOwnerID: cfg.MigrationOwnerID,
			LegacyOwner: cfg.Owner.ID, LegacyTelegramID: cfg.Telegram.OwnerID,
			Accounts:        []accountView{},
			TelegramEnabled: cfg.TelegramEnabled(), TelegramPairing: webConfig.TelegramBotUsername != "" && webConfig.TelegramPairings != nil,
		}
		for _, account := range cfg.Accounts {
			row := accountView{ID: account.ID, Email: account.GoogleEmail, TelegramUserID: account.TelegramUserID, Self: account.ID == self}
			if webConfig.Identities != nil {
				_, _, bound, err := webConfig.Identities.IdentityOf(r.Context(), account.ID)
				if err != nil {
					writeWebError(w, http.StatusInternalServerError, err.Error())
					return
				}
				row.Enrolled = bound
			}
			if webConfig.Sessions != nil {
				live, err := webConfig.Sessions.ActiveSessions(r.Context(), account.ID, now())
				if err != nil {
					writeWebError(w, http.StatusInternalServerError, err.Error())
					return
				}
				row.SignedIn = live > 0
			}
			view.Accounts = append(view.Accounts, row)
		}
		writeJSON(w, view)
	}
}

type accountInput struct {
	ID             string `json:"id"`
	Email          string `json:"email"`
	TelegramUserID int64  `json:"telegram_user_id"`
}

func accountAddRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input accountInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.AddAccount(configPath, config.AccountInput{ID: input.ID, GoogleEmail: input.Email, TelegramUserID: input.TelegramUserID}); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if webConfig.InitializeAccount != nil {
			if err := webConfig.InitializeAccount(input.ID); err != nil {
				writeWebError(w, http.StatusInternalServerError, "account was added but runtime initialization failed; retry after resolving the error: "+err.Error())
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account added.", Detail: "They can sign in with Google now."})
	}
}

func accountEditRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var input accountInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		bound := false
		if webConfig.Identities != nil {
			_, _, enrolled, err := webConfig.Identities.IdentityOf(r.Context(), id)
			if err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			bound = enrolled
		}
		if err := config.EditAccount(configPath, id, config.AccountInput{GoogleEmail: input.Email, TelegramUserID: input.TelegramUserID}, bound); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account updated.", Detail: "Access changed now."})
	}
}

// accountRemoveRoute takes the account out of config and ends its access
// now: identity binding and sessions go, and open streams close. Your own
// account is refused so nobody locks themselves out from a form; the last
// account is refused by config itself.
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
		if webConfig.Identities != nil {
			if err := webConfig.Identities.ResetIdentity(r.Context(), id); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if webConfig.Sessions != nil {
			if err := revokeAccountSessions(r.Context(), webConfig, id); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if webConfig.TelegramPairings != nil {
			if err := webConfig.TelegramPairings.DeleteTelegramPairings(r.Context(), id); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Account removed.", Detail: "They are signed out now. Their private history stays in the database until you delete it."})
	}
}

// accountResetBindingRoute forgets the account's Google identity and ends
// its sessions, so the person enrolls again with the configured address.
func accountResetBindingRoute(webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if webConfig.Identities == nil {
			writeWebError(w, http.StatusNotFound, "not in account mode")
			return
		}
		if _, ok := webConfig.Accounts.Account(id); !ok {
			writeWebError(w, http.StatusNotFound, "account not found")
			return
		}
		if err := webConfig.Identities.ResetIdentity(r.Context(), id); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := revokeAccountSessions(r.Context(), webConfig, id); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if webConfig.TelegramPairings != nil {
			if err := webConfig.TelegramPairings.DeleteTelegramPairings(r.Context(), id); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Binding reset.", Detail: "They are signed out and will enroll again with their configured address on next sign-in."})
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
	getenv := webConfig.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
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

func loginClientSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ClientID        string `json:"client_id"`
			ClientSecretEnv string `json:"client_secret_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.SetGoogleLogin(configPath, input.ClientID, input.ClientSecretEnv); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Sign-in client saved.", Detail: "Restart Eggy to apply."})
	}
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
// once converted, the card edits the list directly.
func accountsConvertRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Accounts             []accountInput `json:"accounts"`
			LoginClientID        string         `json:"login_client_id"`
			LoginClientSecretEnv string         `json:"login_client_secret_env"`
			MigrationOwnerID     string         `json:"migration_owner_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		convert := config.ConvertInput{LoginClientID: input.LoginClientID, LoginClientSecretEnv: input.LoginClientSecretEnv, MigrationOwnerID: input.MigrationOwnerID}
		for _, account := range input.Accounts {
			convert.Accounts = append(convert.Accounts, config.AccountInput{ID: account.ID, GoogleEmail: account.Email, TelegramUserID: account.TelegramUserID})
		}
		if err := config.ConvertToAccounts(configPath, convert); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Converted to accounts.", Detail: "Restart Eggy. The password login stops working; everyone signs in with Google."})
	}
}
