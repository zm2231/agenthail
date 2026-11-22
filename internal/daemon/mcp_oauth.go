package daemon

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	historyMCPOAuth         = "mcp-oauth"
	oauthClientLimit        = 64
	oauthClientStale        = 24 * time.Hour
	oauthRequestBodyLimit   = 16 << 10
	oauthAuthorizationLife  = 10 * time.Minute
	oauthCodeLife           = time.Minute
	oauthOpenAuthorizations = 8
)

var oauthLife = registry.OAuthLifetimes{Access: time.Hour, Refresh: 30 * 24 * time.Hour}

type oauthState struct {
	mu      sync.Mutex
	pending map[string]*oauthPending
	codes   map[string]*oauthCode
}

func newOAuthState() *oauthState {
	return &oauthState{pending: map[string]*oauthPending{}, codes: map[string]*oauthCode{}}
}

// oauthIssuer is the public origin OAuth is served under, or "" while it is off.
func (d *Daemon) oauthIssuer() string {
	issuer, on, err := d.Registry.OAuthIssuer()
	if err != nil {
		d.logRuntimeError("read the OAuth issuer", err)
	}
	if !on {
		return ""
	}
	if !mcpoauth.PastAccess {
		_, _, access, err := d.Registry.AccessSettings()
		if err != nil || access {
			return ""
		}
	}
	return issuer
}

func (d *Daemon) registerOAuth(mux *http.ServeMux) {
	m := d.remoteMCP()
	public := func(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuseTunnelHost(w, r) {
				return
			}
			issuer := d.oauthIssuer()
			if issuer == "" {
				http.NotFound(w, r)
				return
			}
			next(w, r, issuer)
		})
	}
	api := func(method string, next func(http.ResponseWriter, *http.Request, string)) http.Handler {
		return public(func(w http.ResponseWriter, r *http.Request, issuer string) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", method+", OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, MCP-Protocol-Version")
			switch r.Method {
			case http.MethodOptions:
				w.WriteHeader(http.StatusNoContent)
			case method:
				r.Body = http.MaxBytesReader(w, r.Body, oauthRequestBodyLimit)
				next(w, r, issuer)
			default:
				w.Header().Set("Allow", method+", OPTIONS")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}
	resource := api(http.MethodGet, func(w http.ResponseWriter, _ *http.Request, issuer string) {
		mcpoauth.WriteJSON(w, http.StatusOK, mcpoauth.ProtectedResource(issuer))
	})
	mux.Handle(mcpoauth.ProtectedResourcePath, resource)
	mux.Handle(mcpoauth.ResourceMetadataPath, resource)
	mux.Handle(mcpoauth.AuthServerPath, api(http.MethodGet, func(w http.ResponseWriter, _ *http.Request, issuer string) {
		mcpoauth.WriteJSON(w, http.StatusOK, mcpoauth.AuthorizationServer(issuer))
	}))
	mux.Handle(mcpoauth.RegisterPath, api(http.MethodPost, d.oauthRegister))
	mux.Handle(mcpoauth.TokenPath, api(http.MethodPost, m.oauthToken))
	mux.Handle(mcpoauth.RevokePath, api(http.MethodPost, d.oauthRevoke))
	mux.Handle(mcpoauth.AuthorizePath, public(m.oauthAuthorize))
}

func (d *Daemon) oauthRegister(w http.ResponseWriter, r *http.Request, _ string) {
	reg, failure := mcpoauth.ParseRegistration(r.Body)
	if failure != nil {
		mcpoauth.WriteError(w, failure)
		return
	}
	client, err := d.Registry.RegisterOAuthClient(reg.Name, reg.RedirectURIs, oauthClientLimit, oauthClientStale)
	if errors.Is(err, registry.ErrOAuthClientLimit) {
		d.recordOAuth("", "register "+reg.Name, "refused: too many unapproved clients")
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusTooManyRequests, Code: "temporarily_unavailable", Description: "too many clients are waiting for approval; try again later"})
		return
	}
	if err != nil {
		d.logRuntimeError("register an OAuth client", err)
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusInternalServerError, Code: "server_error"})
		return
	}
	d.recordOAuth("", "register "+oauthClientLabel(client.Name, client.ID, mcpoauth.RedirectHost(reg.RedirectURIs[0])), "ok")
	mcpoauth.WriteJSON(w, http.StatusCreated, mcpoauth.RegistrationResponse(client.ID, client.CreatedAt, reg))
}

func (d *Daemon) oauthRevoke(w http.ResponseWriter, r *http.Request, _ string) {
	if err := r.ParseForm(); err != nil {
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusBadRequest, Code: "invalid_request", Description: "the body is not a form"})
		return
	}
	grant, revoked, err := d.Registry.RevokeOAuthToken(r.PostForm.Get("token"), r.PostForm.Get("client_id"))
	if err != nil {
		d.logRuntimeError("revoke an OAuth token", err)
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusInternalServerError, Code: "server_error"})
		return
	}
	if revoked {
		d.recordOAuth(grant.IdentityID, "revoke "+oauthGrantLabel(grant), "revoked by the client")
	}
	w.WriteHeader(http.StatusOK)
}

func (d *Daemon) recordOAuth(identityID, message, result string) {
	if err := d.Registry.RecordHistory(registry.HistoryEntry{Kind: historyMCPOAuth, SourceSessionID: identityID, Message: message, Result: result}); err != nil {
		d.logRuntimeError("record an OAuth event", err)
	}
}

func oauthClientLabel(name, id, redirectHost string) string {
	label := name + " (" + id[:min(len(id), 12)] + "…"
	if redirectHost != "" {
		label += ", redirects to " + redirectHost
	}
	return label + ")"
}

func oauthGrantLabel(grant registry.OAuthGrant) string {
	return "grant " + grant.ID + " of @" + strings.TrimPrefix(grant.Identity, "@") + " to " + grant.ClientName
}
