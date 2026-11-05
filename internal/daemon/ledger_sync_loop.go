package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
)

const (
	ledgerSyncInterval     = 5 * time.Second
	ledgerReplayBatch      = 200
	ledgerReplayBatchBytes = ledger.MaxEventBytes
)

type ledgerSyncHealth struct {
	mu        sync.Mutex
	byProject map[string]ledgerSyncResult
}

type ledgerSyncResult struct {
	At       time.Time
	Error    string
	SyncedAt time.Time
	Head     int64
	Check    ledgerLogCheck
}

func (h *ledgerSyncHealth) recordCheck(key string, check ledgerLogCheck) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byProject == nil {
		h.byProject = map[string]ledgerSyncResult{}
	}
	result := h.byProject[key]
	result.Check = check
	h.byProject[key] = result
}

// record reports whether what a read shows of the sync, its error and the owner's head, changed.
func (h *ledgerSyncHealth) record(key string, head int64, err error) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byProject == nil {
		h.byProject = map[string]ledgerSyncResult{}
	}
	result := h.byProject[key]
	before := result
	result.At, result.Error = time.Now(), ""
	if err != nil {
		result.Error = err.Error()
	} else {
		result.SyncedAt, result.Head = result.At, head
	}
	h.byProject[key] = result
	return result.Error != before.Error || result.Head != before.Head
}

func (d *Daemon) recordLedgerSync(key string, head int64, err error) {
	if d.ledgerSync.record(key, head, err) {
		d.ledgerChanged(key)
	}
}

func (h *ledgerSyncHealth) last(key string) (ledgerSyncResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	result, ok := h.byProject[key]
	return result, ok
}

func (d *Daemon) wakeLedgerSync(host *pairedHost) {
	select {
	case host.ledgerWake <- struct{}{}:
	default:
	}
}

func (d *Daemon) runHostLedgerSync(ctx context.Context, host *pairedHost) {
	ticker := time.NewTicker(ledgerSyncInterval)
	defer ticker.Stop()
	for {
		if state, _ := host.status(); state == hostStateConnected {
			d.syncLedgerHost(ctx, host)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-host.ledgerWake:
		}
	}
}

func (d *Daemon) syncLedgerHost(ctx context.Context, host *pairedHost) {
	keys, err := d.ledgerStore().SyncedProjects(host.record.ID)
	if err != nil {
		d.log.Printf("ledger sync with %s: list projects: %s", host.record.Name, err)
		return
	}
	for _, key := range keys {
		head, err := d.syncLedgerProject(ctx, host, key)
		d.recordLedgerSync(key, head, err)
	}
}

func (d *Daemon) syncLedgerProject(ctx context.Context, host *pairedHost, key string) (int64, error) {
	host.ledgerMu.Lock()
	defer host.ledgerMu.Unlock()
	if !host.ledgerConfirmed[key] {
		if err := d.confirmLedgerOwnerLocked(ctx, host, key); err != nil {
			return 0, err
		}
	}
	store := d.ledgerStore()
	for {
		pending, err := store.PendingReplay(key, ledgerReplayBatch)
		if err != nil || len(pending) == 0 {
			if err != nil {
				return 0, err
			}
			break
		}
		batch := replayBatch(pending)
		var answer ledgerReplayResponse
		if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/replay", nil, ledgerReplayRequest{ProjectKey: key, Events: ledger.ToWire(batch)}, &answer); err != nil {
			return 0, err
		}
		if err := store.MarkReplayed(key, batch, answer.OwnerSeqs); err != nil {
			return 0, err
		}
	}
	return d.pullLedger(ctx, host, key)
}

func replayBatch(pending []ledger.Event) []ledger.Event {
	size := 0
	for i, e := range pending {
		size += len(e.Payload) + 256
		if i > 0 && size > ledgerReplayBatchBytes {
			return pending[:i]
		}
	}
	return pending
}

func (d *Daemon) pullLedger(ctx context.Context, host *pairedHost, key string) (int64, error) {
	store := d.ledgerStore()
	for {
		_, cursor, err := store.Owner(key)
		if err != nil {
			return 0, err
		}
		query := url.Values{"projectKey": {key}, "after": {strconv.FormatInt(cursor, 10)}, "limit": {strconv.Itoa(ledgerPullPage)}}
		var page ledgerEventsResponse
		if err := d.ledgerHostCall(ctx, host, http.MethodGet, "/api/v1/ledger/sync/events", query, nil, &page); err != nil {
			return 0, err
		}
		if err := store.ApplyPulled(key, host.record.ID, ledger.FromWire(key, page.Events)); err != nil {
			return 0, err
		}
		if len(page.Events) < ledgerPullPage {
			d.ledgerSync.recordCheck(key, d.checkLedgerLog(ctx, host, key, page.Digest))
			return page.Head, nil
		}
	}
}

func (d *Daemon) ledgerHostCall(ctx context.Context, host *pairedHost, method, path string, query url.Values, body, out any) error {
	var data []byte
	header := http.Header{}
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
		header.Set("Content-Type", "application/json")
	}
	response, err := host.client.Do(ctx, method, path, query, data, header)
	if err != nil {
		return err
	}
	answer, err := hosts.ReadResponse(response)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(answer, out)
}

func writeLedgerHostError(w http.ResponseWriter, host *pairedHost, err error) {
	var status hosts.StatusError
	if errors.As(err, &status) && status.Status == http.StatusConflict {
		writeAPIError(w, http.StatusConflict, "refused", status.Message)
		return
	}
	writeAPIError(w, http.StatusBadGateway, "ledger_owner_unreachable", "The ledger owner "+host.record.Name+" could not be reached: "+err.Error())
}
