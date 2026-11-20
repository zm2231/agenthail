package daemon

import (
	"context"
	"encoding/json"
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
	credential   registry.SessionCredential
	session      surface.Session
	project      *CatalogProject
	capabilities []string
	ceiling      json.RawMessage
	extension    string
}

func (c *mcpCaller) has(capability string) bool {
	return slices.Contains(c.capabilities, capability)
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
		token := bearerToken(r)
		if caller, ok := d.extensionCaller(token); ok {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, caller)))
			return
		}
		caller, err := withinBound(d, r.Context(), "checking the session credential", mcpCallerBound, func(context.Context) (*mcpCaller, error) {
			return d.credentialCaller(token)
		})
		var stalled *mcpStallError
		switch {
		case errors.As(err, &stalled):
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpAnswerKey{}, socketAnswer{tools: true, refusal: stalled.refusal()})))
		case err != nil && credentialRefused(err):
			writeMCPAuthError(w, err)
		case err != nil:
			d.log.Printf("MCP: checking a session credential failed: %s", err)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpAnswerKey{}, socketAnswer{tools: true, refusal: mcpCheckFailed()})))
		case caller == nil:
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, caller)))
		}
	})
}

var errCallerPermissions = errors.New("reading the session's permissions failed")

// credentialCaller names the session a bearer token belongs to. A nil caller
// with no error is a Codex thread connecting before its token binds.
func (d *Daemon) credentialCaller(token string) (*mcpCaller, error) {
	credential, err := d.authenticateSession(token, time.Now())
	if errors.Is(err, registry.ErrCredentialUnknown) {
		// Codex connects during thread/start, before its token can bind; with no caller every tool call is refused.
		live, liveErr := d.Registry.PendingCredentialLive(token, time.Now())
		if liveErr == nil && live {
			return nil, nil
		}
		if liveErr != nil {
			err = liveErr
		}
	}
	if err != nil {
		return nil, err
	}
	session, err := d.Registry.Session(credential.SessionID)
	if err != nil {
		return nil, err
	}
	caller, err := d.agentCaller(*session)
	if err != nil {
		return nil, errCallerPermissions
	}
	caller.credential = credential
	return caller, nil
}

// A running session keeps its credential past a day unused: its turns may
// start where the daemon never sees them, such as Claude's native peer
// messages.
func (d *Daemon) authenticateSession(token string, now time.Time) (registry.SessionCredential, error) {
	credential, err := d.Registry.AuthenticateSessionToken(token, now)
	var idle registry.CredentialIdleError
	if !errors.As(err, &idle) {
		return credential, err
	}
	session, sessionErr := d.Registry.Session(idle.SessionID)
	if sessionErr != nil {
		d.log.Printf("refused the idle-expired credential of %s: reading the session failed: %s", d.resolveDisplay(idle.SessionID), sessionErr)
		return credential, err
	}
	if !d.sessionRunning(session) {
		d.log.Printf("refused the idle-expired credential of %s: unused for a day and the session is not running", d.resolveDisplay(idle.SessionID))
		return credential, err
	}
	return d.Registry.AuthenticateRunningSessionToken(token, now)
}

func (d *Daemon) sessionRunning(session *surface.Session) bool {
	adapter, err := d.adapters().For(session)
	if err != nil {
		return false
	}
	runner, ok := adapter.(surface.RuntimeRunner)
	return ok && runner.RuntimeRunning(session)
}

// The CLI in a session's shell gets this same caller, so the two cannot disagree.
func (d *Daemon) agentCaller(session surface.Session) (*mcpCaller, error) {
	capabilities, err := d.capabilitiesOf(session.ID)
	if err != nil {
		return nil, err
	}
	ceiling, err := d.Registry.SessionCeiling(session.ID)
	if err != nil {
		return nil, err
	}
	caller := &mcpCaller{session: session, capabilities: capabilities, ceiling: ceiling}
	caller.project, _ = d.sessionProject(session)
	return caller, nil
}

// Only a verdict on the credential answers 401: a client drops a server that answers 401.
func credentialRefused(err error) bool {
	var revoked registry.CredentialRevokedError
	return errors.Is(err, registry.ErrCredentialUnknown) || errors.Is(err, registry.ErrCredentialExpired) || errors.Is(err, registry.ErrCredentialMaxExpired) || errors.As(err, &revoked)
}

func mcpCheckFailed() *mcpRefusal {
	return &mcpRefusal{Code: "busy", Message: "The daemon could not check this session just now; try again in a moment.", Retryable: true}
}

func writeMCPAuthError(w http.ResponseWriter, err error) {
	var revoked registry.CredentialRevokedError
	switch {
	case errors.Is(err, registry.ErrCredentialUnknown):
		writeAPIErrorWith(w, http.StatusUnauthorized, "unauthenticated", "A valid Agenthail session credential is required.", map[string]any{"retryable": false})
	case errors.Is(err, registry.ErrCredentialExpired):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_expired", "This session's credential expired after a day unused while the session was not running; it works again once Agenthail next sends the session a message or the session is resumed.", map[string]any{"retryable": true, "detail": map[string]any{"reason": "idle"}})
	case errors.Is(err, registry.ErrCredentialMaxExpired):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_expired", "This session's credential reached its 30-day limit; the session gets a new one when it is next started or resumed.", map[string]any{"retryable": false, "detail": map[string]any{"reason": "max"}})
	case errors.As(err, &revoked):
		writeAPIErrorWith(w, http.StatusUnauthorized, "credential_revoked", "This session's credential was revoked ("+revoked.Reason+").", map[string]any{"retryable": false, "detail": map[string]any{"reason": revoked.Reason}})
	}
}
