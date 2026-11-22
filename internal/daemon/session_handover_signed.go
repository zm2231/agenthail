package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

const handoverFetchTimeout = 10 * time.Second

type servedHandovers struct {
	Handovers []hosts.Handover `json:"handovers"`
}

func (d *Daemon) deliverHandovers(ctx context.Context, host *pairedHost) {
	handovers, err := d.Registry.UndeliveredHandovers(host.record.ID)
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" handovers", err)
		return
	}
	for _, handover := range handovers {
		body, _ := json.Marshal(handover)
		response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/session-move/handover", nil, body, http.Header{"Content-Type": []string{"application/json"}})
		if err == nil {
			_, err = hosts.ReadResponse(response)
		}
		var refused hosts.StatusError
		if err != nil && !(errors.As(err, &refused) && refused.Status == http.StatusConflict) {
			d.logRuntimeError("move "+handover.MoveID+" handover", err)
			return
		}
		if err != nil {
			d.logRuntimeError("move "+handover.MoveID+" handover", err)
		}
		if err := d.Registry.MarkHandoverDelivered(handover.MoveID, time.Now()); err != nil {
			d.logRuntimeError("move "+handover.MoveID+" handover", err)
		}
	}
}

func (d *Daemon) apiMoveHandoverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var handover hosts.Handover
	if err := decodeAPIV1JSON(w, r, &handover, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The body must be a signed handover.")
		return
	}
	source, _ := requestingHost(r.Context())
	err := d.Registry.KeepHandover(handover, source, d.hostIdentity().ID)
	var refused registry.HandoverRefusedError
	var conflict registry.MoveConflictError
	switch {
	case errors.Is(err, registry.ErrMoveNotFound):
		writeAPIError(w, http.StatusConflict, "move_unknown", "This host has no record of that move.")
	case errors.As(err, &refused):
		writeAPIError(w, http.StatusConflict, "handover_refused", refused.Reason)
	case errors.As(err, &conflict):
		writeAPIError(w, http.StatusConflict, "move_not_resumed", "The move is "+conflict.State+" here, not resumed, so it takes no handover.")
	case err != nil:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
	default:
		writeDashboardJSON(w, http.StatusOK, map[string]bool{"kept": true})
	}
}

func (d *Daemon) apiSessionHandoverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	chain, err := d.Registry.ServedHandovers(r.URL.Query().Get("sessionId"))
	switch {
	case err != nil:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
	case len(chain) == 0:
		writeAPIError(w, http.StatusNotFound, "not_found", "This host holds no signed handover for that session.")
	default:
		writeDashboardJSON(w, http.StatusOK, servedHandovers{Handovers: chain})
	}
}

func (d *Daemon) claimSignedHandover(host *pairedHost, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), handoverFetchTimeout)
	defer cancel()
	var served servedHandovers
	if err := host.client.GetJSON(ctx, "/api/v1/session-handover", url.Values{"sessionId": []string{sessionID}}, &served); err != nil {
		var missing hosts.StatusError
		if !errors.As(err, &missing) || missing.Status != http.StatusNotFound {
			d.logRuntimeError("host "+host.record.Name+" handover of "+sessionID, err)
		}
		return
	}
	d.catalog.mu.Lock()
	owner, handed, err := d.Registry.HandOverSigned(served.Handovers, sessionID, host.record.ID)
	if err == nil && handed {
		d.publishCommittedHostWriteLocked("host " + host.record.Name + " signed handover " + sessionID)
	}
	d.catalog.mu.Unlock()
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" handover of "+sessionID, err)
		return
	}
	if from, found := d.pairedHost(owner); handed && found {
		d.afterHandOver(from, host, sessionID)
	}
}
