package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

func (d *Daemon) identityOAuthHandler(w http.ResponseWriter, r *http.Request) {
	p := principalFromRequest(r)
	if p.Kind != principalDevice && (p.Kind != principalHost || !p.forThePerson()) {
		writeAPIError(w, http.StatusForbidden, "person_only", "Which identities may sign in over OAuth changes from agenthail identity oauth in the person's own terminal or from a paired device.")
		return
	}
	d.serveIdentityOAuth(w, r, p.actor())
}

func (d *Daemon) serveIdentityOAuth(w http.ResponseWriter, r *http.Request, actor string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var request daemonapi.IdentityOAuthRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&request); err != nil || request.Identity == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "send an identity and enabled")
		return
	}
	result, err := d.setIdentityOAuth(r.Context(), actor, request)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

// setIdentityOAuth tells the person who changed it, so a change that did not come from them is seen.
func (d *Daemon) setIdentityOAuth(ctx context.Context, actor string, request daemonapi.IdentityOAuthRequest) (daemonapi.IdentityOAuthResult, error) {
	name := strings.TrimPrefix(request.Identity, "@")
	revoked, err := d.Registry.SetIdentityOAuth(name, request.Enabled)
	if err != nil {
		return daemonapi.IdentityOAuthResult{}, err
	}
	identityID := ""
	if remote, err := d.Registry.RemoteIdentityNamed(name); err == nil {
		identityID = remote.IdentityID
	}
	change := "turned OAuth sign-in on for @" + name
	if !request.Enabled {
		change = fmt.Sprintf("turned OAuth sign-in off for @%s and revoked %d grant(s)", name, revoked)
	}
	d.recordOAuth(identityID, change, "by "+actor)
	d.tellPerson(ctx, "OAuth sign-in changed", actor+" "+change+".", "", "identity.oauth.changed")
	return daemonapi.IdentityOAuthResult{Identity: name, Enabled: request.Enabled, Revoked: revoked}, nil
}
