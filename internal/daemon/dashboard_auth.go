package daemon

import (
	"net/http"

	"github.com/zm2231/agenthail/internal/hosts"
)

const dashboardCookieMaxAge = 365 * 24 * 60 * 60

func (dashboard *dashboardServer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !dashboard.authorized(r) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "dashboard access token required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "forbidden", "cross-origin dashboard request rejected")
			return
		}
		next(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
}

func (d *Daemon) dashboardHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hosts.ResponseHeader, d.hostIdentity().ID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self' https: wss:; media-src blob:; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
