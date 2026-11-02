package daemon

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerLogCheck struct {
	At              time.Time
	Through         int64
	OwnerSHA256     string
	Match           bool
	FirstDifference int64
	Error           string
}

func (d *Daemon) checkLedgerLog(ctx context.Context, host *pairedHost, key string, owner *ledger.LogDigest) ledgerLogCheck {
	check := ledgerLogCheck{At: time.Now()}
	if owner == nil {
		check.Error = host.record.Name + " sent no log digest with its last page"
		return check
	}
	check.Through, check.OwnerSHA256 = owner.Through, owner.SHA256
	local, err := d.ledgerStore().LogDigest(key, owner.Through)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	if local == *owner {
		check.Match = true
		return check
	}
	if last, ok := d.ledgerSync.last(key); ok && last.Check.Through == owner.Through && last.Check.OwnerSHA256 == owner.SHA256 && last.Check.FirstDifference > 0 {
		check.FirstDifference = last.Check.FirstDifference
		return check
	}
	first, err := d.firstLedgerDifference(ctx, host, key, owner.Through)
	if err != nil {
		check.Error = "the logs differ through owner position " + strconv.FormatInt(owner.Through, 10) + "; locating the first difference failed: " + err.Error()
		return check
	}
	check.FirstDifference = first
	return check
}

func (d *Daemon) firstLedgerDifference(ctx context.Context, host *pairedHost, key string, through int64) (int64, error) {
	store := d.ledgerStore()
	agree, differ := int64(0), through
	for differ-agree > 1 {
		mid := agree + (differ-agree)/2
		var owner ledger.LogDigest
		if err := d.ledgerHostCall(ctx, host, http.MethodGet, "/api/v1/ledger/sync/digest", url.Values{"projectKey": {key}, "through": {strconv.FormatInt(mid, 10)}}, nil, &owner); err != nil {
			return 0, err
		}
		local, err := store.LogDigest(key, mid)
		if err != nil {
			return 0, err
		}
		if local == owner {
			agree = mid
		} else {
			differ = mid
		}
	}
	return differ, nil
}

func (d *Daemon) apiLedgerSyncDigestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	query := r.URL.Query()
	through, err := strconv.ParseInt(query.Get("through"), 10, 64)
	if err != nil || through < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "through must be an owner position.")
		return
	}
	digest, err := d.ledgerStore().OwnerLogDigest(query.Get("projectKey"), through)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeVerbatimJSON(w, http.StatusOK, digest)
}
