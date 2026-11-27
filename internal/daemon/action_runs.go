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
	if reservation.Acquired {
		if a.running == nil {
			a.running = map[string]bool{}
		}
		a.running[actionRunID(principal, key)] = true
		return reservation, nil
	}
	reservation.Receipt, err = a.settleIfInterrupted(r, principal, key, requestHash, canonical, reservation.Receipt)
	return reservation, err
}

// The caller holds a.mu.
func (a *actionRuns) settleIfInterrupted(r *registry.Registry, principal, key, requestHash string, canonical []byte, receipt registry.ActionReceipt) (registry.ActionReceipt, error) {
	if receipt.Status != registry.ActionReceiptPending || receipt.RequestHash != requestHash || a.running[actionRunID(principal, key)] {
		return receipt, nil
	}
	interrupted, err := encodeActionReplay(interruptedActionReply(canonical, receipt.CreatedAt))
	if err != nil {
		return receipt, err
	}
	// A failed write still answers with the outcome unknown; the next resend tries the write again.
	_ = r.CompleteActionReceipt(principal, key, requestHash, interrupted, time.Now().UTC())
	receipt.Status, receipt.Receipt = registry.ActionReceiptCompleted, interrupted
	return receipt, nil
}

// current reads a receipt as claim would see it, settling one whose run ended
// without an outcome, but never reserves it.
func (a *actionRuns) current(r *registry.Registry, principal, key, requestHash string, canonical []byte) (registry.ActionReceipt, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	receipt, found, err := r.ActionReceipt(principal, key)
	if err != nil || !found {
		return receipt, found, err
	}
	receipt, err = a.settleIfInterrupted(r, principal, key, requestHash, canonical, receipt)
	return receipt, true, err
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
