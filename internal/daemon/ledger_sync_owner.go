package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerOwnerRefusal struct {
	message    string
	keyRefused bool
}

func (e ledgerOwnerRefusal) Error() string { return e.message }

func (d *Daemon) apiLedgerOwnerHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerOwnerRequest
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	if req.ProjectKey == "" || req.Host == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "projectKey and host are required.")
		return
	}
	host, ok := d.pairedHost(req.Host)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "host_not_found", "No paired host "+req.Host+"; see agenthail hosts.")
		return
	}
	result, err := d.assignLedgerOwner(r.Context(), host, req.ProjectKey)
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

// assignLedgerOwner makes host the owner of this host's ledger under key,
// in the two phases that never leave only one of them thinking it owns it.
func (d *Daemon) assignLedgerOwner(ctx context.Context, host *pairedHost, key string) (ledgerOwnerResponse, error) {
	if err := d.hostTransit(ctx, host); err != nil {
		return ledgerOwnerResponse{}, err
	}
	host.ledgerMu.Lock()
	defer host.ledgerMu.Unlock()
	store := d.ledgerStore()
	if err := store.CanAssignOwner(key, host.record.ID); err != nil {
		return ledgerOwnerResponse{}, ledgerActionError(err)
	}
	moves, err := store.HoldsHistory(key)
	if err != nil {
		return ledgerOwnerResponse{}, ledgerActionError(err)
	}
	adopting, err := store.HoldsUnsharedIDs(key)
	if err != nil {
		return ledgerOwnerResponse{}, ledgerActionError(err)
	}
	var claimed ledgerOwnerResponse
	if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/claim", nil, ledgerClaimRequest{ProjectKey: key, Adopting: adopting}, &claimed); err != nil {
		return ledgerOwnerResponse{}, ledgerHostActionError(host, err)
	}
	if claimed.Owner != host.record.ID {
		d.abortLedgerClaim(ctx, host, key)
		return ledgerOwnerResponse{}, actionFailed(http.StatusConflict, "owner_mismatch", host.record.Name+" answered as "+claimed.Owner+", not as itself; nothing was recorded.")
	}
	if err := store.AssignOwner(key, host.record.ID); err != nil {
		d.abortLedgerClaim(ctx, host, key)
		return ledgerOwnerResponse{}, ledgerActionError(err)
	}
	result := ledgerOwnerResponse{ProjectKey: key, Owner: host.record.ID, OwnerName: host.record.Name, Adopted: moves}
	var refusal ledgerOwnerRefusal
	switch err := d.confirmLedgerOwnerLocked(ctx, host, key); {
	case err == nil:
		result.Confirmed = true
	case errors.As(err, &refusal) && refusal.keyRefused:
		return ledgerOwnerResponse{}, actionFailed(http.StatusBadRequest, ledgerUnknownKeyCode, refusal.message)
	case errors.As(err, &refusal):
		return ledgerOwnerResponse{}, actionFailed(http.StatusConflict, "refused", refusal.message)
	default:
		result.Pending = err.Error()
	}
	d.wakeLedgerSync(host)
	return result, nil
}

func (d *Daemon) abortLedgerClaim(ctx context.Context, host *pairedHost, key string) {
	if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/abort", nil, ledgerClaimRequest{ProjectKey: key}, nil); err != nil {
		d.log.Printf("ledger owner %s for %s: abort the pending claim: %s; it expires on its own", host.record.Name, key, err)
	}
}

func (d *Daemon) confirmLedgerOwnerLocked(ctx context.Context, host *pairedHost, key string) error {
	adopting, err := d.ledgerStore().HoldsUnsharedIDs(key)
	if err != nil {
		return err
	}
	err = d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/confirm", nil, ledgerClaimRequest{ProjectKey: key, Adopting: adopting}, nil)
	var status hosts.StatusError
	var keyRefused ledgerKeyRefused
	switch {
	case err == nil:
		if host.ledgerConfirmed == nil {
			host.ledgerConfirmed = map[string]bool{}
		}
		host.ledgerConfirmed[key] = true
		return nil
	case errors.As(err, &keyRefused):
		if rerr := d.ledgerStore().ReleaseOwner(key, host.record.ID); rerr != nil {
			return fmt.Errorf("%s, and this host could not take the ledger for %s back: %w", keyRefused.message, key, rerr)
		}
		d.abortLedgerClaim(ctx, host, key)
		d.log.Printf("ledger owner %s refused to confirm %s: %s; this host keeps the ledger again", host.record.Name, key, keyRefused.message)
		return ledgerOwnerRefusal{message: keyRefused.message + " This host keeps the ledger for " + key + " again.", keyRefused: true}
	case !errors.As(err, &status) || status.Status != http.StatusConflict:
		return err
	}
	if rerr := d.ledgerStore().ReleaseOwner(key, host.record.ID); rerr != nil {
		return fmt.Errorf("%s refused to keep the ledger for %s (%s), and this host could not take it back: %w", host.record.Name, key, status.Message, rerr)
	}
	d.log.Printf("ledger owner %s refused %s: %s; this host keeps the ledger again", host.record.Name, key, status.Message)
	return ledgerOwnerRefusal{message: fmt.Sprintf("%s refused to keep the ledger for %s: %s. Nothing was recorded there; this host keeps the ledger again.", host.record.Name, key, status.Message)}
}

func (d *Daemon) apiLedgerSyncClaimHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerClaimRequest
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	requester, _ := requestingHost(r.Context())
	if err := d.ledgerStore().CheckClaim(req.ProjectKey, requester, req.Adopting); err != nil {
		writeLedgerError(w, err)
		return
	}
	if holder, ok := d.ledgerClaims.reserve(req.ProjectKey, requester); !ok {
		name := holder.requester
		if other, found := d.pairedHost(holder.requester); found {
			name = other.record.Name
		}
		writeAPIError(w, http.StatusConflict, "refused", fmt.Sprintf("%s is waiting for %s to confirm its claim on %s until %s; nothing was recorded.", d.hostIdentity().Name, name, req.ProjectKey, holder.expires.UTC().Format("15:04:05Z")))
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerOwnerResponse{ProjectKey: req.ProjectKey, Owner: d.hostIdentity().ID, OwnerName: d.hostIdentity().Name})
}

func (d *Daemon) apiLedgerSyncConfirmHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerClaimRequest
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	requester, _ := requestingHost(r.Context())
	if claim, ok := d.ledgerClaims.lookup(req.ProjectKey); ok && claim.requester != requester {
		writeAPIError(w, http.StatusConflict, "refused", "Another host's claim on "+req.ProjectKey+" is pending here.")
		return
	}
	if err := d.ledgerStore().ClaimOwnership(req.ProjectKey, requester, req.Adopting); err != nil {
		var refusal *ledger.Refusal
		if errors.As(err, &refusal) {
			d.ledgerClaims.release(req.ProjectKey, requester)
		}
		writeLedgerError(w, err)
		return
	}
	d.ledgerClaims.release(req.ProjectKey, requester)
	writeDashboardJSON(w, http.StatusOK, ledgerOwnerResponse{ProjectKey: req.ProjectKey, Owner: d.hostIdentity().ID, OwnerName: d.hostIdentity().Name, Confirmed: true})
}

func (d *Daemon) apiLedgerSyncAbortHandler(w http.ResponseWriter, r *http.Request) {
	var req ledgerClaimRequest
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	requester, _ := requestingHost(r.Context())
	d.ledgerClaims.release(req.ProjectKey, requester)
	writeDashboardJSON(w, http.StatusOK, struct{}{})
}
