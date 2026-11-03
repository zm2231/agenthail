package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpCallerKey struct{}

type mcpCaller struct {
	credential registry.SessionCredential
	session    surface.Session
	project    *CatalogProject
}

func (c *mcpCaller) has(capability string) bool {
	return slices.Contains(c.credential.Capabilities, capability)
}

func mcpCallerFrom(ctx context.Context) *mcpCaller {
	caller, _ := ctx.Value(mcpCallerKey{}).(*mcpCaller)
	return caller
}

var proxiedRequestHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip", "Tailscale-User-Login"}

// A tailnet proxy reaches the daemon from loopback too, so its forwarding headers disqualify a request.
func localReach(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return false
	}
	for _, header := range proxiedRequestHeaders {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	return true
}

func (d *Daemon) mcpAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localReach(r) {
			writeAPIError(w, http.StatusForbidden, "remote_refused", "Session credentials are accepted only from this machine.")
			return
		}
		credential, err := d.Registry.AuthenticateSessionToken(bearerToken(r), time.Now())
		if err != nil {
			writeMCPAuthError(w, err)
			return
		}
		session, err := d.Registry.Session(credential.SessionID)
		if err != nil {
			writeMCPAuthError(w, err)
			return
		}
		caller := &mcpCaller{credential: credential, session: *session}
		caller.project, _ = d.sessionProject(*session)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, caller)))
	})
}

func writeMCPAuthError(w http.ResponseWriter, err error) {
	var revoked registry.CredentialRevokedError
	switch {
	case errors.Is(err, registry.ErrCredentialUnknown):
		writeAPIErrorWith(w, http.StatusUnauthorized, "unauthenticated", "A valid Agenthail session credential is required.", map[string]any{"retryable": false})
	case errors.Is(err, registry.ErrCredentialExpired):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_expired", "This session's credential expired after a day unused; it works again after the session's next turn.", map[string]any{"retryable": true, "detail": map[string]any{"reason": "idle"}})
	case errors.Is(err, registry.ErrCredentialMaxExpired):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_expired", "This session's credential reached its 30-day limit; the session gets a new one when it is next started or resumed.", map[string]any{"retryable": false, "detail": map[string]any{"reason": "max"}})
	case errors.As(err, &revoked):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_revoked", "This session's credential was revoked ("+revoked.Reason+").", map[string]any{"retryable": false, "detail": map[string]any{"reason": revoked.Reason}})
	default:
		writeAPIErrorWith(w, http.StatusUnauthorized, "unauthenticated", "The session credential could not be checked.", map[string]any{"retryable": true})
	}
}
