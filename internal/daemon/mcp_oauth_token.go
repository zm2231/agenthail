package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
)

type oauthCode struct {
	pending *oauthPending
	expires time.Time
	used    bool
	reused  bool
	grantID string
}

func oauthCodeKey(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func (m *remoteMCP) issueOAuthCodeLocked(pending *oauthPending, now time.Time) (string, error) {
	code, err := randomOAuthSecret()
	if err != nil {
		return "", err
	}
	m.oauth.codes[oauthCodeKey(code)] = &oauthCode{pending: pending, expires: now.Add(oauthCodeLife)}
	return code, nil
}

func invalidGrant(description string) *mcpoauth.Error {
	return &mcpoauth.Error{Status: http.StatusBadRequest, Code: "invalid_grant", Description: description}
}

func (m *remoteMCP) oauthToken(w http.ResponseWriter, r *http.Request, issuer string) {
	if err := r.ParseForm(); err != nil {
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusBadRequest, Code: "invalid_request", Description: "the body is not a form"})
		return
	}
	form := r.PostForm
	if !mcpoauth.ResourceMatches(form.Get("resource"), issuer) {
		mcpoauth.WriteError(w, &mcpoauth.Error{Status: http.StatusBadRequest, Code: "invalid_target", Description: "resource must be " + mcpoauth.Resource(issuer)})
		return
	}
	var tokens registry.OAuthTokens
	var failure *mcpoauth.Error
	switch form.Get("grant_type") {
	case "authorization_code":
		tokens, failure = m.exchangeOAuthCode(form.Get("code"), form.Get("client_id"), form.Get("redirect_uri"), form.Get("code_verifier"))
	case "refresh_token":
		tokens, failure = m.refreshOAuth(form.Get("refresh_token"), form.Get("client_id"))
	default:
		failure = &mcpoauth.Error{Status: http.StatusBadRequest, Code: "unsupported_grant_type", Description: "only authorization_code and refresh_token are served"}
	}
	if failure != nil {
		mcpoauth.WriteError(w, failure)
		return
	}
	mcpoauth.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":  tokens.Access,
		"token_type":    "Bearer",
		"expires_in":    int(time.Until(tokens.AccessExpires).Seconds()),
		"refresh_token": tokens.Refresh,
	})
}

// A code presented twice was copied, so the grant it already produced is revoked (OAuth 2.1 section 4.1.3).
func (m *remoteMCP) exchangeOAuthCode(code, clientID, redirectURI, verifier string) (registry.OAuthTokens, *mcpoauth.Error) {
	d := m.d
	s := m.oauth
	s.mu.Lock()
	entry, ok := s.codes[oauthCodeKey(code)]
	if !ok || code == "" {
		s.mu.Unlock()
		return registry.OAuthTokens{}, invalidGrant("the code is unknown or expired")
	}
	if entry.used {
		entry.reused = true
		grantID := entry.grantID
		s.mu.Unlock()
		if grantID != "" {
			m.revokeReusedCode(grantID)
		}
		return registry.OAuthTokens{}, invalidGrant("the code was already used")
	}
	pending := entry.pending
	switch {
	case time.Now().After(entry.expires):
		s.mu.Unlock()
		return registry.OAuthTokens{}, invalidGrant("the code expired")
	case clientID != pending.client.ID:
		s.mu.Unlock()
		return registry.OAuthTokens{}, invalidGrant("the code was issued to another client")
	case redirectURI != pending.req.RedirectURI && (pending.req.RedirectGiven || redirectURI != ""):
		s.mu.Unlock()
		return registry.OAuthTokens{}, invalidGrant("redirect_uri differs from the authorization request's")
	case !mcpoauth.VerifyS256(verifier, pending.req.Challenge):
		s.mu.Unlock()
		return registry.OAuthTokens{}, invalidGrant("code_verifier does not match the code_challenge")
	}
	entry.used = true
	s.mu.Unlock()
	tokens, err := d.Registry.IssueOAuthGrant(pending.identityID, pending.client.ID, oauthLife)
	if err != nil {
		d.logRuntimeError("issue an OAuth grant", err)
		return registry.OAuthTokens{}, invalidGrant("the grant could not be issued")
	}
	d.recordOAuth(pending.identityID, "issue "+oauthGrantLabel(tokens.Grant), "ok")
	s.mu.Lock()
	entry.grantID = tokens.Grant.ID
	reused := entry.reused
	s.mu.Unlock()
	if reused {
		m.revokeReusedCode(tokens.Grant.ID)
		return registry.OAuthTokens{}, invalidGrant("the code was already used")
	}
	return tokens, nil
}

func (m *remoteMCP) revokeReusedCode(grantID string) {
	grant, err := m.d.Registry.RevokeOAuthGrant(grantID)
	if err != nil {
		m.d.logRuntimeError("revoke a grant whose code was used twice", err)
		return
	}
	m.d.recordOAuth(grant.IdentityID, "revoke "+oauthGrantLabel(grant), "its code was used twice")
}

func (m *remoteMCP) refreshOAuth(refresh, clientID string) (registry.OAuthTokens, *mcpoauth.Error) {
	d := m.d
	tokens, err := d.Registry.RefreshOAuthGrant(refresh, clientID, oauthLife)
	switch {
	case errors.Is(err, registry.ErrOAuthRefreshReused):
		d.recordOAuth(tokens.Grant.IdentityID, "revoke "+oauthGrantLabel(tokens.Grant), "a spent refresh token was presented")
		return registry.OAuthTokens{}, invalidGrant(err.Error())
	case errors.Is(err, registry.ErrOAuthUnknown):
		return registry.OAuthTokens{}, invalidGrant("the refresh token is unknown or expired")
	case err != nil:
		d.logRuntimeError("refresh an OAuth grant", err)
		return registry.OAuthTokens{}, &mcpoauth.Error{Status: http.StatusInternalServerError, Code: "server_error"}
	}
	d.recordOAuth(tokens.Grant.IdentityID, "refresh "+oauthGrantLabel(tokens.Grant), "ok")
	return tokens, nil
}
