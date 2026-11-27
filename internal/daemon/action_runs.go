package daemon

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// actionRuns knows which keyed actions this daemon is running. A run marks its
// receipt before it leaves this set, so a pending receipt outside it belongs to
// a run that ended without one: a daemon that stopped mid-run, or a receipt
// write that failed.
type actionRuns struct {
	mu      sync.Mutex
	running map[string]bool
}

func actionRunID(principal, key string) string { return principal + "\x00" + key }

func (a *actionRuns) claim(r *registry.Registry, principal, key, requestHash string, canonical []byte) (registry.ActionReceiptReservation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	reservation, err := r.ReserveActionReceipt(principal, key, requestHash, time.Now().UTC())
	if err != nil {
		return reservation, err
	}
	id := actionRunID(principal, key)
	if reservation.Acquired {
		if a.running == nil {
			a.running = map[string]bool{}
		}
		a.running[id] = true
		return reservation, nil
	}
	if reservation.Receipt.Status != registry.ActionReceiptPending || reservation.Receipt.RequestHash != requestHash || a.running[id] {
		return reservation, nil
	}
	interrupted, err := encodeActionReplay(interruptedActionReply(canonical, reservation.Receipt.CreatedAt))
	if err != nil {
		return reservation, err
	}
	// A failed write still answers with the outcome unknown; the next resend tries the write again.
	_ = r.CompleteActionReceipt(principal, key, requestHash, interrupted, time.Now().UTC())
	reservation.Receipt.Status, reservation.Receipt.Receipt = registry.ActionReceiptCompleted, interrupted
	return reservation, nil
}

func (a *actionRuns) done(principal, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, actionRunID(principal, key))
}

func interruptedActionReply(canonical []byte, since string) actionReply {
	var request struct {
		Action    string `json:"action"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(canonical, &request)
	check := "Check what it was meant to change"
	extra := map[string]any{"since": since}
	if request.Action == "send" {
		check = "Check the session's transcript for the message"
	}
	if request.SessionID != "" {
		extra["sessionId"] = request.SessionID
	}
	return replyWith(func(w http.ResponseWriter) {
		writeAPIErrorWith(w, http.StatusConflict, "action_outcome_unknown", "This request was interrupted before its outcome was recorded, so it may have happened. "+check+" before sending it again as a new request.", extra)
	})
}
