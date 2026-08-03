package web

import (
	"io"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/plugins/auth/session"
	"github.com/nigelteosw/eggy/plugins/webui"
)

// Safe mode is what Eggy serves when it could not start: a config.yaml that
// does not parse, a setting that fails validation, an adapter that refuses to
// wire. The daemon used to exit on any of those, which on a container
// deployment is unrecoverable -- config.yaml lives on a mounted volume that
// only the daemon can reach, and a crash-looping container has no shell to
// reach it with. So the process stays up with one job: let the owner read the
// error and repair the file that caused it.
//
// The page itself is the same web UI as always, from website/: safe mode
// serves the built bundle and answers GET /api/mode with "safe", which is how
// the app knows to render its repair screen instead of chat. The bundle is
// compiled into the binary, so it is present in exactly the situations safe
// mode is for -- a second hand-written page in Go would be a second design to
// maintain for no gain in availability.
//
// What safe mode will not do is start the agent, touch memory, or serve chat.
// The only state it can change is config.yaml, and only through a body that
// LoadConfig has already accepted.

// SafeMode is everything the safe-mode surface needs. Web carries the owner's
// login credential and cookie signing key, which come from the environment and
// so are available even when no config loaded.
type SafeMode struct {
	ConfigPath string
	// Failure is why startup failed. It is shown to the authenticated owner
	// verbatim -- it is the whole reason this surface exists.
	Failure error
	Getenv  func(string) string
	// Repaired is called after config.yaml has been replaced with a config
	// that loads. It is how safe mode hands control back: the supervisor
	// retries startup rather than waiting for someone to redeploy.
	Repaired func()
	Web      WebUIConfig
}

// NewSafeModeHandler serves the repair page and the routes behind it. Mount it
// as Routes.Web with Routes.Ready returning the same failure, so /healthz
// stays 200 -- the process is alive, and a platform that reroutes away from an
// unhealthy container would take the repair page down with it -- while
// /readyz reports the deployment as not serving.
func NewSafeModeHandler(mode SafeMode) http.Handler {
	now := mode.Web.Now
	if now == nil {
		now = time.Now
	}
	getenv := mode.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	// The login throttle keys on client IP through the same helper the normal
	// UI uses. TrustedProxyHops comes from a config that by definition did not
	// load, so it is zero here: X-Forwarded-For is ignored and every request
	// behind a proxy shares one bucket. That is the fail-safe direction -- the
	// throttle only delays attempts by seconds, so a shared bucket slows an
	// attacker without locking the owner out of the repair page.
	throttle := session.NewLoginThrottle(now)
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	// Safe mode reports the default theme rather than the configured one: the
	// config that would name it is the config that failed to load.
	mux.HandleFunc("GET /api/mode", writeMode(modeSafe, nil))
	mux.HandleFunc("POST /api/login", handleWebLogin(mode.Web, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/safemode", requireWebSession(mode.Web, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, webResult{
			State: webError, Title: "Eggy did not start.", Detail: failureText(mode.Failure),
			Fields: []webField{{Label: "config", Value: mode.ConfigPath}},
			Next:   []string{"Fix the config below and save. Eggy retries startup as soon as it loads."},
		})
	}))
	mux.Handle("GET /api/config/raw", requireWebSession(mode.Web, now, func(w http.ResponseWriter, _ *http.Request) {
		body, err := config.ReadConfigText(mode.ConfigPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	mux.Handle("POST /api/config/raw", requireWebSession(mode.Web, now, func(w http.ResponseWriter, r *http.Request) {
		// The file is small by construction and the writer is the
		// authenticated owner, but the cap keeps a stuck or hostile client
		// from filling the volume.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeWebError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		if err := config.ReplaceConfig(mode.ConfigPath, body, getenv); err != nil {
			// A rejected config is never written: the owner still has the file
			// they had, plus the reason this one would not have started.
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if mode.Repaired != nil {
			mode.Repaired()
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Config saved.", Detail: "Eggy is starting up again. Reload in a few seconds."})
	}))
	// Every other API route belongs to the Eggy that is not running. Reporting
	// them unavailable is more honest than the 404 the asset server would
	// give, which reads as "this deployment does not have that feature".
	unavailableInSafeMode := func(w http.ResponseWriter, _ *http.Request) {
		writeWebError(w, http.StatusServiceUnavailable, "Eggy is in safe mode: config.yaml did not load")
	}
	mux.HandleFunc("GET /api/", unavailableInSafeMode)
	mux.HandleFunc("/", unavailableInSafeMode)
	return mux
}

func failureText(failure error) string {
	if failure == nil {
		return "unknown startup failure"
	}
	return failure.Error()
}
