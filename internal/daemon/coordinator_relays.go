package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

type coordinatorRelaysRequest struct {
	ProjectID string `json:"projectId"`
	From      string `json:"from"`
	To        string `json:"to"`
}

func (d *Daemon) registerCoordinatorRelaysAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/coordinator-relays", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiCoordinatorRelaysHandler)))
}

// apiCoordinatorRelaysHandler is a paired host asking, once its replace of
// this host's lead has fully taken effect, that the relays of the lead this
// host cleared go to the session that replaced it. It is a path of its own
// rather than a field of coordinator-clear, because a person's action routed
// through a paired host arrives as that host too.
func (d *Daemon) apiCoordinatorRelaysHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for coordinator relays.")
		return
	}
	hostID, err := d.boundHost(r.Context())
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	var request coordinatorRelaysRequest
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := d.followPeerLead(hostID, request)
	writeActionResult(w, result, err)
}

// followPeerLead moves the routes of this host's lead that the requesting
// host's replace cleared to that host's new lead. Only the lead this host
// last cleared for the project, with no coordinator set here since, and only
// to a session this host mirrors from the requesting host.
func (d *Daemon) followPeerLead(hostID string, request coordinatorRelaysRequest) (actionResult, error) {
	if request.ProjectID == "" || request.From == "" || request.To == "" {
		return actionResult{}, invalidRequest("projectId, from and to are required")
	}
	if !d.coordinatorMu.TryLock() {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_busy", fmt.Sprintf("%s is changing a coordinator; try again", d.hostIdentity().Name))
	}
	defer d.coordinatorMu.Unlock()
	if owner, err := d.Registry.SessionHost(request.From); err != nil || owner != "" {
		return actionResult{}, actionFailed(http.StatusNotFound, "session_not_found", fmt.Sprintf("%s has no session %s", d.hostIdentity().Name, request.From))
	}
	if owner, err := d.Registry.SessionHost(request.To); err != nil || owner != hostID {
		return actionResult{}, actionFailed(http.StatusNotFound, "session_not_found", fmt.Sprintf("%s has no session %s from the requesting host yet", d.hostIdentity().Name, request.To))
	}
	if _, err := d.Registry.Coordinator(request.ProjectID); err == nil {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_changed", "the project has a coordinator on this host again")
	}
	history, err := d.Registry.CoordinatorHistory(request.ProjectID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if len(history) == 0 || history[len(history)-1].Action != "cleared" || history[len(history)-1].PreviousSessionID != request.From {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_changed", fmt.Sprintf("%s is not the lead this host last cleared for the project", request.From))
	}
	if err := d.Registry.MoveRelayRoutes(request.From, request.To); err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	d.publishEvent("state.changed", request.To, map[string]string{"source": "relay"})
	return actionOK(map[string]any{"ok": true}), nil
}

// handOverPeerRelays asks the paired host whose lead a replace cleared to
// move that lead's relays to sessionID. The replace already stands, so a
// host that cannot is logged and its relays stay on its former lead.
func (d *Daemon) handOverPeerRelays(ctx context.Context, previous registry.CoordinatorSpec, sessionID string) {
	_, from, _ := hosts.Split(previous.SessionID)
	_, projectID, _ := hosts.Split(previous.ProjectID)
	if from == "" || sessionID == "" {
		return
	}
	host, found := d.pairedHost(previous.HostID)
	if !found {
		return
	}
	body, err := json.Marshal(coordinatorRelaysRequest{ProjectID: projectID, From: from, To: sessionID})
	if err != nil {
		return
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/coordinator-relays", nil, body, http.Header{actionIdempotencyHeader: []string{"coordinator-relays-" + uuid.NewString()}})
	if err == nil {
		_, err = hosts.ReadResponse(response)
	}
	if err != nil {
		d.log.Printf("coordinator %s: the relays of %s on host %s stay on it: %s", previous.ProjectID, from, host.record.Name, err)
	}
}
