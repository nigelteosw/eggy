package bootstrap

import (
	"net/http"
)

func NewHTTPHandler(ready func() error, telegram, googleStart, googleCallback http.Handler) http.Handler {
	return NewHTTPHandlerAt("/webhooks/telegram", ready, telegram, googleStart, googleCallback)
}

func NewHTTPHandlerAt(telegramPath string, ready func() error, telegram, googleStart, googleCallback http.Handler) http.Handler {
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
	if googleStart != nil {
		mux.Handle("GET /auth/google", googleStart)
	}
	if googleCallback != nil {
		mux.Handle("GET /auth/google/callback", googleCallback)
	}
	return mux
}

func unavailable(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "adapter unavailable", http.StatusServiceUnavailable)
}
