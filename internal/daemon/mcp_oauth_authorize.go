package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	oauthApprove = "approve"
	oauthDecline = "decline"
	oauthExpired = "expired"
)

type oauthPending struct {
	req        mcpoauth.AuthorizeRequest
	client     registry.OAuthClient
	identityID string
	identity   string
	askID      string
	match      string
	expires    time.Time
	decision   string
}

func (m *remoteMCP) oauthAuthorize(w http.ResponseWriter, r *http.Request, issuer string) {
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Has("pending"):
		m.oauthWait(w, r, issuer, r.URL.Query().Get("pending"))
	case r.Method == http.MethodGet:
		req, client, ok := m.oauthRequest(w, r, r.URL.Query(), issuer)
		if ok {
			mcpoauth.ChoosePage{Client: client.Name, RedirectHost: mcpoauth.RedirectHost(req.RedirectURI), Action: mcpoauth.AuthorizePath, Hidden: mcpoauth.HiddenParams(r.URL.Query())}.Write(w)
		}
	case r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, oauthRequestBodyLimit)
		if err := r.ParseForm(); err != nil {
			mcpoauth.MessagePage{Title: "Something went wrong", Text: "The form could not be read. Start again from the app."}.Write(w, http.StatusBadRequest)
			return
		}
		req, client, ok := m.oauthRequest(w, r, r.PostForm, issuer)
		if ok {
			m.oauthAsk(w, r, req, client, strings.TrimPrefix(strings.TrimSpace(r.PostForm.Get("identity")), "@"))
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *remoteMCP) oauthRequest(w http.ResponseWriter, r *http.Request, q url.Values, issuer string) (mcpoauth.AuthorizeRequest, registry.OAuthClient, bool) {
	var client registry.OAuthClient
	req, failure, sendBack := mcpoauth.ParseAuthorize(q, issuer, func(id string) ([]string, bool) {
		found, err := m.d.Registry.OAuthClient(id)
		client = found
		return found.RedirectURIs, err == nil
	})
	switch {
	case failure != nil && sendBack:
		http.Redirect(w, r, mcpoauth.ErrorRedirect(req, issuer, failure), http.StatusFound)
	case failure != nil:
		mcpoauth.MessagePage{Title: "This app cannot connect", Text: failure.Description + "."}.Write(w, failure.Status)
	}
	return req, client, failure == nil
}

// An unknown name gets the same page as a known one, so the page tells no one which identities exist.
func (m *remoteMCP) oauthAsk(w http.ResponseWriter, r *http.Request, req mcpoauth.AuthorizeRequest, client registry.OAuthClient, name string) {
	d := m.d
	id, err := randomOAuthSecret()
	if err != nil {
		d.logRuntimeError("start an OAuth approval", err)
		mcpoauth.MessagePage{Title: "Something went wrong", Text: "Start again from the app."}.Write(w, http.StatusInternalServerError)
		return
	}
	pending := &oauthPending{req: req, client: client, identity: name, match: matchCode(), expires: time.Now().Add(oauthAuthorizationLife)}
	s := m.oauth
	s.mu.Lock()
	m.sweepOAuthLocked(time.Now())
	if m.openOAuthApprovalsLocked() >= oauthOpenAuthorizations {
		s.mu.Unlock()
		mcpoauth.MessagePage{Title: "Too many requests are waiting", Text: "Try again in a few minutes."}.Write(w, http.StatusTooManyRequests)
		return
	}
	s.pending[id] = pending
	s.mu.Unlock()
	d.background.start(func(ctx context.Context) {
		for _, at := range []time.Time{pending.expires, pending.expires.Add(oauthCodeLife)} {
			select {
			case <-time.After(time.Until(at)):
				s.mu.Lock()
				m.sweepOAuthLocked(time.Now())
				s.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	})
	if remote, err := d.Registry.RemoteIdentityNamed(name); err == nil {
		m.raiseOAuthAsk(id, pending, remote)
	} else {
		d.recordOAuth("", "authorize "+oauthClientLabel(client.Name, client.ID, mcpoauth.RedirectHost(req.RedirectURI))+" as @"+clip(name, 40), "no identity with remote access by that name")
	}
	http.Redirect(w, r, mcpoauth.AuthorizePath+"?pending="+url.QueryEscape(id), http.StatusSeeOther)
}

func (m *remoteMCP) raiseOAuthAsk(id string, pending *oauthPending, remote registry.RemoteIdentity) {
	d := m.d
	role := "the " + d.remoteRoleName(remote.IdentityID) + " role"
	host := mcpoauth.RedirectHost(pending.req.RedirectURI)
	ask, err := d.Registry.RaiseAsk(registry.Ask{SessionID: remote.IdentityID, Origin: registry.AskOriginOAuthGrant, RoutedTo: registry.AskToPerson,
		Title: clipText(fmt.Sprintf("%s asks to connect as @%s", pending.client.Name, remote.Name), registry.MaxAskTitle),
		Questions: []registry.AskQuestion{{
			Question:    clipText(fmt.Sprintf("Let %s, which returns to %s, act as @%s with %s? Approve only if the page you opened shows code %s.", pending.client.Name, host, remote.Name, role, pending.match), registry.MaxAskQuestion),
			Options:     []registry.AskOption{{ID: oauthApprove, Label: "Approve", Does: clipText("It can call Agenthail's tools as @"+remote.Name+" until you revoke it", registry.MaxAskDoes)}, {ID: oauthDecline, Label: "Decline"}},
			Recommended: oauthDecline,
		}}}, time.Now())
	if err != nil {
		d.logRuntimeError("raise the OAuth approval", err)
		return
	}
	s := m.oauth
	s.mu.Lock()
	if current, ok := s.pending[id]; ok && current == pending {
		pending.identityID, pending.askID = remote.IdentityID, ask.ID
		s.mu.Unlock()
	} else {
		s.mu.Unlock()
		d.withdrawOAuthAsk(ask)
		return
	}
	d.announceAsk(ask)
	if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
		d.log.Printf("oauth: mark %s told: %s", ask.ID, err)
	}
	d.publishAsk(remote.IdentityID)
	d.recordOAuth(remote.IdentityID, "authorize "+oauthClientLabel(pending.client.Name, pending.client.ID, host)+" as @"+remote.Name, "asked the person ("+ask.ID+")")
}

func (m *remoteMCP) oauthWait(w http.ResponseWriter, r *http.Request, issuer, id string) {
	s := m.oauth
	now := time.Now()
	s.mu.Lock()
	pending, ok := s.pending[id]
	if !ok {
		s.mu.Unlock()
		mcpoauth.MessagePage{Title: "This request has ended", Text: "Start again from the app."}.Write(w, http.StatusGone)
		return
	}
	switch {
	case pending.decision == oauthApprove:
		delete(s.pending, id)
		code, err := m.issueOAuthCodeLocked(pending, now)
		s.mu.Unlock()
		if err != nil {
			m.d.logRuntimeError("issue an OAuth code", err)
			http.Redirect(w, r, mcpoauth.ErrorRedirect(pending.req, issuer, &mcpoauth.Error{Code: "server_error", Description: "the code could not be issued"}), http.StatusFound)
			return
		}
		http.Redirect(w, r, mcpoauth.CodeRedirect(pending.req, issuer, code), http.StatusFound)
	case pending.decision == oauthDecline || !now.Before(pending.expires):
		m.expireOAuthPending(pending)
		delete(s.pending, id)
		s.mu.Unlock()
		description := "the person declined"
		if pending.decision == oauthExpired {
			description = "no one approved in time"
		}
		http.Redirect(w, r, mcpoauth.ErrorRedirect(pending.req, issuer, &mcpoauth.Error{Code: "access_denied", Description: description}), http.StatusFound)
	default:
		s.mu.Unlock()
		mcpoauth.WaitingPage{Client: pending.client.Name, Identity: pending.identity, Code: pending.match, Refresh: mcpoauth.AuthorizePath + "?pending=" + url.QueryEscape(id)}.Write(w)
	}
}

func (m *remoteMCP) openOAuthApprovalsLocked() int {
	open := 0
	for _, pending := range m.oauth.pending {
		if pending.decision == "" {
			open++
		}
	}
	return open
}

func (m *remoteMCP) sweepOAuthLocked(now time.Time) {
	s := m.oauth
	for id, pending := range s.pending {
		if now.Before(pending.expires) {
			continue
		}
		m.expireOAuthPending(pending)
		if !now.Before(pending.expires.Add(oauthCodeLife)) {
			delete(s.pending, id)
		}
	}
	for hash, code := range s.codes {
		if now.After(code.expires.Add(oauthCodeLife)) {
			delete(s.codes, hash)
		}
	}
}

// The page keeps its expired approval a little longer, so its next refresh can send the client access_denied.
func (m *remoteMCP) expireOAuthPending(pending *oauthPending) {
	if pending.decision != "" {
		return
	}
	pending.decision = oauthExpired
	if pending.askID == "" {
		return
	}
	if ask, found, err := m.d.Registry.Ask(pending.askID); err == nil && found {
		m.d.withdrawOAuthAsk(ask)
	}
	m.d.recordOAuth(pending.identityID, "authorize "+pending.client.Name+" as @"+pending.identity, "expired unanswered")
}

func (d *Daemon) answerOAuthGrant(ask registry.Ask, response surface.Response, p principal, by string) (actionResult, error) {
	if !answeredByThePerson(p, by) {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person lets an app connect as an identity")
	}
	if len(response.Choices) != 1 || (response.Choices[0] != oauthApprove && response.Choices[0] != oauthDecline) {
		return actionResult{}, invalidRequest("choose approve or decline")
	}
	choice := response.Choices[0]
	s := d.remoteMCP().oauth
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending *oauthPending
	for _, candidate := range s.pending {
		if candidate.askID == ask.ID {
			pending = candidate
		}
	}
	if pending == nil || pending.decision != "" || time.Now().After(pending.expires) {
		d.withdrawOAuthAsk(ask)
		return actionResult{}, staleRequest()
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: choice}}, registry.AskToPerson, p.actor(), time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	pending.decision = choice
	if err := d.Registry.RecordAskApplied(ask.ID, "applied", "", time.Now()); err != nil {
		d.log.Printf("oauth: record the answer to %s: %s", ask.ID, err)
	}
	d.sendWithdraws()
	d.publishAsk(ask.SessionID)
	outcome := map[string]string{oauthApprove: "approved", oauthDecline: "declined"}[choice]
	d.recordOAuth(pending.identityID, "authorize "+pending.client.Name+" as @"+pending.identity, outcome+" by "+p.actor())
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": outcome}}), nil
}

func (d *Daemon) withdrawOAuthAsk(ask registry.Ask) {
	if withdrawn, err := d.Registry.WithdrawAsk(ask.ID, time.Now()); err != nil {
		d.log.Printf("oauth: withdraw %s: %s", ask.ID, err)
	} else if withdrawn {
		d.withdrawAsk(ask.ID)
		d.publishAsk(ask.SessionID)
	}
}

// Approvals wait in memory, so one left open by an earlier run can no longer be completed.
func (d *Daemon) withdrawOAuthAsks() {
	open, err := d.Registry.OpenAsks()
	if err != nil {
		d.log.Printf("oauth: read open asks: %s", err)
		return
	}
	for _, ask := range open {
		if ask.Origin == registry.AskOriginOAuthGrant {
			d.withdrawOAuthAsk(ask)
		}
	}
}

func (d *Daemon) remoteRoleName(identityID string) string {
	record, err := d.Registry.Caller(identityID)
	if err != nil {
		return "remote"
	}
	return d.oauthCallerRole(record).Name
}

func matchCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}

func randomOAuthSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}
