package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/plugins/auth/session"
)

const (
	setupSessionCookie = "eggy_setup_session"
	setupMaxBodyBytes  = 32 << 10
)

// SetupMode is the complete, in-memory authority available during a fresh
// installation. It is discarded when the supervisor starts normal mode.
type SetupMode struct {
	ConfigPath    string
	PublicBaseURL string
	TokenHash     [32]byte
	SessionKey    []byte
	Expires       time.Time
	Now           func() time.Time
	Validate      func(config.SetupInput) (map[string]bool, error)
	Complete      func(config.SetupInput) error
	Completed     func()
}

type setupState struct {
	mu        sync.Mutex
	tokenUsed bool
	completed bool
}

// NewSetupModeHandler exposes only the setup app, credential exchange,
// validation, and completion. All normal API routes remain unavailable.
func NewSetupModeHandler(mode SetupMode) http.Handler {
	now := mode.Now
	if now == nil {
		now = time.Now
	}
	state := &setupState{}
	throttle := session.NewLoginThrottle(now)
	mux := http.NewServeMux()
	mux.Handle("GET /", webUIHandler())
	mux.HandleFunc("GET /api/mode", writeMode("setup", nil, ""))
	mux.HandleFunc("POST /api/setup/session", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, 0)
		if throttle.Delay(ip) > 0 {
			setupUnauthorized(w)
			return
		}
		var request struct {
			Token string `json:"token"`
		}
		if err := decodeSetupJSON(w, r, &request); err != nil {
			throttle.RecordFailure(ip)
			setupUnauthorized(w)
			return
		}
		hash := sha256.Sum256([]byte(request.Token))
		state.mu.Lock()
		valid := !state.tokenUsed && now().Before(mode.Expires) && subtle.ConstantTimeCompare(hash[:], mode.TokenHash[:]) == 1
		if valid {
			state.tokenUsed = true
		}
		state.mu.Unlock()
		if !valid {
			throttle.RecordFailure(ip)
			setupUnauthorized(w)
			return
		}
		throttle.Reset(ip)
		http.SetCookie(w, &http.Cookie{
			Name: setupSessionCookie, Value: session.SignSession(mode.SessionKey, mode.Expires),
			Path: "/", HttpOnly: true, Secure: setupSecure(mode.PublicBaseURL),
			SameSite: http.SameSiteStrictMode, Expires: mode.Expires,
		})
		w.WriteHeader(http.StatusNoContent)
	})
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(setupSessionCookie)
			if err != nil || !session.VerifySession(mode.SessionKey, cookie.Value, now()) || !now().Before(mode.Expires) {
				writeWebError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			if !setupSameOrigin(mode.PublicBaseURL, r) {
				writeWebError(w, http.StatusForbidden, "request did not come from the Eggy setup page")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /api/setup/validate", guard(func(w http.ResponseWriter, r *http.Request) {
		var input config.SetupInput
		if err := decodeSetupJSON(w, r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		variables := map[string]bool{}
		if mode.Validate != nil {
			var err error
			variables, err = mode.Validate(input)
			if err != nil {
				writeSetupValidationError(w, err, variables)
				return
			}
		}
		writeSetupVariables(w, http.StatusOK, variables, "")
	}))
	mux.HandleFunc("POST /api/setup/complete", guard(func(w http.ResponseWriter, r *http.Request) {
		var input config.SetupInput
		if err := decodeSetupJSON(w, r, &input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.completed {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if mode.Complete == nil {
			writeWebError(w, http.StatusServiceUnavailable, "setup completion is unavailable")
			return
		}
		if err := mode.Complete(input); err != nil {
			writeSetupValidationError(w, err, nil)
			return
		}
		state.completed = true
		w.WriteHeader(http.StatusNoContent)
		if mode.Completed != nil {
			mode.Completed()
		}
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			_, pattern := mux.Handler(r)
			if pattern == "" || pattern == "GET /" {
				writeWebError(w, http.StatusServiceUnavailable, "Eggy is waiting for setup")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func decodeSetupJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, setupMaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func setupUnauthorized(w http.ResponseWriter) {
	writeWebError(w, http.StatusUnauthorized, "setup credential is invalid or has expired")
}

func setupSecure(publicBaseURL string) bool {
	u, err := url.Parse(publicBaseURL)
	return err == nil && strings.EqualFold(u.Scheme, "https")
}

func setupSameOrigin(publicBaseURL string, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	public, err := url.Parse(publicBaseURL)
	return err == nil && strings.EqualFold(u.Scheme, public.Scheme) && strings.EqualFold(u.Host, public.Host)
}

func writeSetupVariables(w http.ResponseWriter, status int, variables map[string]bool, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Variables map[string]bool `json:"variables"`
		Detail    string          `json:"detail,omitempty"`
	}{Variables: variables, Detail: detail})
}

func writeSetupValidationError(w http.ResponseWriter, err error, variables map[string]bool) {
	writeSetupVariables(w, http.StatusBadRequest, variables, err.Error())
}
