package daemon

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) forwardHostAction(w http.ResponseWriter, r *http.Request, host *pairedHost, request map[string]any, body []byte) {
	key := r.Header.Get(actionIdempotencyHeader)
	if key == "" {
		key = "fwd-" + uuid.NewString()
	}
	header := http.Header{actionIdempotencyHeader: []string{key}, ActionActorHeader: []string{actionActor(r)}}
	response, err := host.client.Do(r.Context(), http.MethodPost, "/api/v1/actions", nil, body, header)
	if err == nil {
		answer, readErr := readHostAnswer(host, response)
		if readErr == nil {
			answer.write(w)
			return
		}
		err = readErr
	}
	action, _ := request["action"].(string)
	sessionID, _ := request["sessionId"].(string)
	if action != "send" || sessionID == "" {
		writeHostError(w, host, err)
		return
	}
	message, _ := request["message"].(string)
	var notSent hosts.NotSentError
	sent := !errors.As(err, &notSent)
	queueID, queueErr := d.Registry.QueueHostDelivery(sessionID, message, key, body, sent)
	if queueErr != nil {
		writeHostError(w, host, errors.Join(err, queueErr))
		return
	}
	d.refreshHostQueueCounts()
	receipt := delivery.Receipt{Evidence: surface.EvidenceQueued, Status: "queued", SessionID: hosts.Qualify(host.record.ID, sessionID), QueueID: queueID, Detail: fmt.Sprintf("host %s is unreachable; queued until it reconnects", host.record.Name)}
	if sent {
		receipt.Evidence = surface.EvidenceUnknown
		receipt.Status = "submitted"
		receipt.Detail = fmt.Sprintf("host %s did not answer; the outcome is unknown until it confirms this request", host.record.Name)
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": receipt})
}

func (d *Daemon) queueHostRelay(w http.ResponseWriter, r *http.Request, session *surface.Session, message, sourceID, busyDelivery string, hops int) {
	if _, fromHost := requestingHost(r.Context()); !fromHost {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "relayHops is set only by a paired host forwarding a relay")
		return
	}
	if hops < 1 || hops > maxRelayHops {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("a relay may take at most %d hops", maxRelayHops))
		return
	}
	key := r.Header.Get(actionIdempotencyHeader)
	if key == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "a forwarded relay needs its idempotency key")
		return
	}
	queueID, err := d.Registry.QueueRelayMessageWithOptions(session.ID, message, "host-relay:"+key, hops, surface.SendOptions{SourceSessionID: sourceID, BusyDelivery: busyDelivery})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": delivery.Receipt{Evidence: surface.EvidenceQueued, Status: "queued", SessionID: session.ID, QueueID: queueID, Detail: "relay queued"}})
}

func (d *Daemon) apiRelaysHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for relays.")
		return
	}
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "relays_unavailable", err.Error())
		return
	}
	for index := range routes {
		for _, id := range []*string{&routes[index].FromSession, &routes[index].ToSession} {
			if owner, ownerErr := d.Registry.SessionHost(*id); ownerErr == nil && owner != "" {
				*id = hosts.Qualify(owner, *id)
			}
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"relays": routes})
}
