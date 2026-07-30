package web

import (
	"net/http"
)

// Routes are the optional handlers the HTTP surface mounts. Every field is
// optional: a nil handler means that route is not served (the Telegram
// webhook path is the one exception -- it reports unavailable rather than
// 404, so a misconfigured deployment is distinguishable from a wrong URL).
//
// This is a struct rather than a positional parameter list because the list
// had already grown a trailing variadic http.Handler to bolt on the MCP OAuth
// callback. A variadic slot is a hole, not a parameter: it accepts any number
// of handlers, silently ignores all but the first, and gives the reader no
// name for what it carries.
type Routes struct {
	Ready          func() error
	TelegramPath   string
	Telegram       http.Handler
	MCPCallback    http.Handler
	GoogleStart    http.Handler
	GoogleCallback http.Handler
	Web            http.Handler
}

// DefaultTelegramWebhookPath is used when Routes.TelegramPath is empty.
const DefaultTelegramWebhookPath = "/webhooks/telegram"

func NewHTTPHandler(routes Routes) http.Handler {
	telegramPath := routes.TelegramPath
	if telegramPath == "" {
		telegramPath = DefaultTelegramWebhookPath
	}
	ready, telegram := routes.Ready, routes.Telegram
	web := routes.Web
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if ready != nil {
			if err := ready(); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	if telegram != nil {
		mux.Handle("POST "+telegramPath, telegram)
	} else {
		mux.HandleFunc(telegramPath, unavailable)
	}
	if routes.MCPCallback != nil {
		mux.Handle("GET /auth/mcp/{server}/callback", routes.MCPCallback)
	}
	if routes.GoogleStart != nil {
		mux.Handle("GET /auth/google", routes.GoogleStart)
	}
	if routes.GoogleCallback != nil {
		mux.Handle("GET /auth/google/callback", routes.GoogleCallback)
	}
	if web != nil {
		mux.Handle("/", web)
	}
	return mux
}

func unavailable(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "adapter unavailable", http.StatusServiceUnavailable)
}
