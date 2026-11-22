package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) identitySettingsHandler(w http.ResponseWriter, r *http.Request) {
	p := principalFromRequest(r)
	if p.Kind != principalDevice && (p.Kind != principalHost || !p.forThePerson()) {
		writeAPIError(w, http.StatusForbidden, "person_only", "An identity's settings change from agenthail identity set in the person's own terminal or from a paired device.")
		return
	}
	d.serveIdentitySettings(w, r, p.actor())
}

func (d *Daemon) serveIdentitySettings(w http.ResponseWriter, r *http.Request, actor string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var request daemonapi.IdentitySettingsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&request); err != nil || request.Identity == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "send an identity and a sendMode")
		return
	}
	result, err := d.setIdentitySettings(r.Context(), actor, request)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

func (d *Daemon) setIdentitySettings(ctx context.Context, actor string, request daemonapi.IdentitySettingsRequest) (daemonapi.IdentitySettingsResult, error) {
	name := strings.TrimPrefix(request.Identity, "@")
	if err := d.Registry.SetIdentitySendMode(name, request.SendMode); err != nil {
		return daemonapi.IdentitySettingsResult{}, err
	}
	change := "set @" + name + "'s default send mode to " + request.SendMode
	identityID := ""
	if identity, err := d.Registry.AgentIdentityNamed(name); err == nil {
		identityID = identity.SessionID
	}
	if err := d.Registry.RecordHistory(registry.HistoryEntry{Kind: "identity-settings", SourceSessionID: identityID, Message: change, Result: "by " + actor}); err != nil {
		d.logRuntimeError("record an identity settings change", err)
	}
	d.tellPerson(ctx, "Identity settings changed", actor+" "+change+".", "", "identity.settings.changed")
	return daemonapi.IdentitySettingsResult{Identity: name, SendMode: request.SendMode}, nil
}
