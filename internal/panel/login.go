// Client identity for the login throttle. The login routes themselves are
// in account_auth.go (password) and login_link.go (Telegram /web links).
package panel

import (
	"net"
	"net/http"
	"strings"
)

// clientIP identifies the client the login throttle counts against. With
// hops > 0 it walks X-Forwarded-For from the right, skipping the hops-1
// proxies Eggy is known to sit behind, so the returned address is the one the
// outermost trusted proxy actually observed -- entries further left are
// attacker-supplied and never used. Anything unexpected (no header, a chain
// shorter than the configured hop count, an unparseable entry) falls back to
// RemoteAddr rather than trusting a value that does not fit the deployment.
func clientIP(r *http.Request, hops int) string {
	remote := remoteHost(r)
	if hops <= 0 {
		return remote
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	index := len(forwarded) - hops
	if index < 0 {
		return remote
	}
	candidate := strings.TrimSpace(forwarded[index])
	if net.ParseIP(candidate) == nil {
		return remote
	}
	return candidate
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
