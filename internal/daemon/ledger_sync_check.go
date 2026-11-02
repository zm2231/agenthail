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
	Match           bool
	FirstDifference int64
	Error           string
	localBefore     string
	localAt         string
}

func (d *Daemon) checkLedgerLog(ctx context.Context, host *pairedHost, key string, owner *ledger.LogDigest) ledgerLogCheck {
	check := ledgerLogCheck{At: time.Now()}
	if owner == nil {
		check.Error = host.record.Name + " sent no log digest with its last page"
		return check
	}
	check.Through = owner.Through
	store := d.ledgerStore()
	local, err := store.LogDigest(key, owner.Through)
	if err != nil {
		check.Error = err.Error()
		return check
	}
	if local == *owner {
		check.Match = true
		return check
	}
	first := int64(0)
	if last, ok := d.ledgerSync.last(key); ok && last.Check.FirstDifference > 0 && last.Check.FirstDifference <= owner.Through {
		first = last.Check.FirstDifference
	}
	if first > 0 {
		before, at, err := localAround(store, key, first)
		if err != nil {
			check.Error = err.Error()
			return check
		}
		if last, _ := d.ledgerSync.last(key); before == last.Check.localBefore && at == last.Check.localAt {
			check.FirstDifference, check.localBefore, check.localAt = first, before, at
			return check
		}
	}
	if first, err = d.firstLedgerDifference(ctx, host, key, owner.Through); err != nil {
		check.Error = "the logs differ through owner position " + strconv.FormatInt(owner.Through, 10) + "; locating the first difference failed: " + err.Error()
		return check
	}
	check.FirstDifference = first
	if check.localBefore, check.localAt, err = localAround(store, key, first); err != nil {
		check.Error = err.Error()
	}
	return check
}

// localAround is this host's digest just before and at the first difference; the owner's prefix through it never changes, so equal values here mean the difference is still there.
func localAround(store *ledger.Store, key string, first int64) (string, string, error) {
	before, err := store.LogDigest(key, first-1)
	if err != nil {
		return "", "", err
	}
	at, err := store.LogDigest(key, first)
	return before.SHA256, at.SHA256, err
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
