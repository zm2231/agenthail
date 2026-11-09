package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) forwardHostAction(ctx context.Context, p principal, key string, host *pairedHost, request map[string]any, body []byte) actionReply {
	if key == "" {
		key = "fwd-" + uuid.NewString()
	}
	header := http.Header{actionIdempotencyHeader: []string{key}, ActionActorHeader: []string{p.actor()}}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, header)
	if err == nil {
		answer, readErr := readHostAnswer(host, response)
		if readErr == nil {
			if next, found := d.pairedHost(movedTo(answer)); found && next != host && !redirectedAfterMove(ctx) {
				if transitErr := d.hostTransit(ctx, next); transitErr == nil {
					return d.forwardHostAction(context.WithValue(ctx, redirectedAfterMoveKey{}, true), p, key, next, request, body)
				}
			}
			return replyWith(answer.write)
		}
		err = readErr
	}
	action, _ := request["action"].(string)
	sessionID, _ := request["sessionId"].(string)
	if action != "send" || sessionID == "" {
		return replyWith(func(w http.ResponseWriter) { writeHostError(w, host, err) })
	}
	message, _ := request["message"].(string)
	var notSent hosts.NotSentError
	sent := !errors.As(err, &notSent)
	queueID, queueErr := d.Registry.QueueHostDelivery(sessionID, message, key, body, sent)
	if queueErr != nil {
		return replyWith(func(w http.ResponseWriter) { writeHostError(w, host, errors.Join(err, queueErr)) })
	}
	d.refreshHostQueueCounts()
	receipt := delivery.Receipt{Evidence: surface.EvidenceQueued, Status: "queued", SessionID: hosts.Qualify(host.record.ID, sessionID), QueueID: queueID, Detail: fmt.Sprintf("host %s is unreachable; queued until it reconnects", host.record.Name)}
	if sent {
		receipt.Evidence = surface.EvidenceUnknown
		receipt.Status = "submitted"
		receipt.Detail = fmt.Sprintf("host %s did not answer; the outcome is unknown until it confirms this request", host.record.Name)
	}
	return replyWith(func(w http.ResponseWriter) {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": receipt})
	})
}

func (d *Daemon) doHostRelay(ctx context.Context, session *surface.Session, message, sourceID, busyDelivery string, hops int) (actionResult, error) {
	if _, err := d.boundHost(ctx); err != nil {
		return actionResult{}, err
	}
	if hops < 1 || hops > maxRelayHops {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("a relay may take at most %d hops", maxRelayHops))
	}
	key := actionKey(ctx)
	if key == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "a forwarded relay needs its idempotency key")
	}
	queueID, err := d.Registry.QueueRelayMessageWithOptions(session.ID, message, "host-relay:"+key, hops, surface.SendOptions{SourceSessionID: sourceID, BusyDelivery: busyDelivery})
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "result": delivery.Receipt{Evidence: surface.EvidenceQueued, Status: "queued", SessionID: session.ID, QueueID: queueID, Detail: "relay queued"}}), nil
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

type redirectedAfterMoveKey struct{}

func redirectedAfterMove(ctx context.Context) bool {
	redirected, _ := ctx.Value(redirectedAfterMoveKey{}).(bool)
	return redirected
}
