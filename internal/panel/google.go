package panel

import "net/http"

// googleLoginPath is where the Google card's Connect button points.
const googleLoginPath = "/auth/google"

// GoogleCallbackPath is where a Web client's login returns, under
// server.public_base_url. Bootstrap builds the redirect from it and the card
// shows it as the address to register, so the two cannot disagree.
const GoogleCallbackPath = "/auth/google/callback"

// webGoogleLoginRoute starts the shared Google login and sends the owner's
// browser to Google. The login returns to the callback route bootstrap mounts,
// which is why this exists only for a Web client: a Desktop client's login
// ends on a dead loopback page that only /google login can take a paste of.
func webGoogleLoginRoute(runtime GoogleLoginStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorizationURL, err := runtime.BeginLogin(r.Context())
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, authorizationURL, http.StatusFound)
	}
}
