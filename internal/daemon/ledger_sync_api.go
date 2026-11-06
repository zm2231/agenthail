package daemon

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const (
	ledgerReplayBodyLimit = 2 * ledger.MaxEventBytes
	ledgerPullPage        = 500
)

type ledgerClaimRequest struct {
	ProjectKey string `json:"projectKey"`
	Adopting   bool   `json:"adopting"`
}

type ledgerReplayRequest struct {
	ProjectKey string             `json:"projectKey"`
	Events     []ledger.WireEvent `json:"events"`
}

type ledgerReplayResponse struct {
	OwnerSeqs []int64 `json:"ownerSeqs"`
}

type ledgerEventsResponse struct {
	Events []ledger.WireEvent `json:"events"`
	Head   int64              `json:"head"`
	Digest *ledger.LogDigest  `json:"digest,omitempty"`
}

type ledgerSyncView struct {
	ProjectKey string     `json:"projectKey"`
	Owner      string     `json:"owner"`
	OwnerName  string     `json:"ownerName"`
	OwnerState string     `json:"ownerState"`
	Cursor     int64      `json:"cursor"`
	OwnerHead  int64      `json:"ownerHead"`
	Lag        int64      `json:"lag"`
	Unreplayed int        `json:"unreplayed"`
	SyncedAt   *time.Time `json:"syncedAt,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	Log        string     `json:"log"`
	LogThrough int64      `json:"logThrough,omitempty"`
	FirstDiff  int64      `json:"firstDifference,omitempty"`
	LogError   string     `json:"logError,omitempty"`
}

type ledgerOwnerRequest struct {
	ProjectKey string `json:"projectKey"`
	Host       string `json:"host"`
}

type ledgerOwnerResponse struct {
	ProjectKey string `json:"projectKey"`
	Owner      string `json:"owner"`
	OwnerName  string `json:"ownerName"`
	Adopted    bool   `json:"adopted"`
	Confirmed  bool   `json:"confirmed"`
	Pending    string `json:"pending,omitempty"`
}

func (d *Daemon) registerLedgerSyncRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ledger/sync/claim", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiLedgerSyncClaimHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/confirm", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiLedgerSyncConfirmHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/abort", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiLedgerSyncAbortHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/moves", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiLedgerSyncMovesHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/replay", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiLedgerSyncReplayHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/events", d.apiV1Guard(dashboard, "read", d.hostOnly(d.apiLedgerSyncEventsHandler)))
	mux.HandleFunc("/api/v1/ledger/sync/digest", d.apiV1Guard(dashboard, "read", d.hostOnly(d.apiLedgerSyncDigestHandler)))
	mux.HandleFunc("/api/v1/ledger/owner", d.apiV1Guard(dashboard, "control", d.apiLedgerOwnerHandler))
	mux.HandleFunc("/api/v1/ledger/sync/status", d.apiV1Guard(dashboard, "read", d.apiLedgerSyncStatusHandler))
}

func (d *Daemon) apiLedgerSyncReplayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var req ledgerReplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, ledgerReplayBodyLimit)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	hostID, _ := requestingHost(r.Context())
	seqs, err := d.ledgerStore().AcceptEvents(req.ProjectKey, hostID, ledger.FromWire(req.ProjectKey, req.Events))
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, ledgerReplayResponse{OwnerSeqs: seqs})
}

func (d *Daemon) apiLedgerSyncEventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	query := r.URL.Query()
	after, _ := strconv.ParseInt(query.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit < 1 || limit > ledgerPullPage {
		limit = ledgerPullPage
	}
	key := query.Get("projectKey")
	events, err := d.ledgerStore().ExportEvents(key, after, limit)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	head, err := d.ledgerStore().LatestSeq(key)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	page := ledgerEventsResponse{Events: ledger.ToWire(events), Head: head}
	if len(events) < limit {
		through := after
		if len(events) > 0 {
			through = events[len(events)-1].Seq
		}
		digest, err := d.ledgerStore().LogDigest(key, through)
		if err != nil {
			writeLedgerError(w, err)
			return
		}
		page.Digest = &digest
	}
	writeVerbatimJSON(w, http.StatusOK, page)
}

func (d *Daemon) apiLedgerSyncStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	views, err := d.ledgerSyncViews()
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, views)
}

func (d *Daemon) ledgerSyncViews() ([]ledgerSyncView, error) {
	store := d.ledgerStore()
	keys, err := store.RemotelyOwned()
	if err != nil {
		return nil, err
	}
	views := []ledgerSyncView{}
	for _, key := range keys {
		status, err := store.Status(key)
		if err != nil {
			return nil, err
		}
		view := ledgerSyncView{ProjectKey: key, Owner: status.Owner, OwnerName: status.Owner, OwnerState: "not paired", Cursor: status.Cursor, Unreplayed: status.Unreplayed, Log: "unchecked"}
		if host, ok := d.pairedHost(status.Owner); ok {
			view.OwnerName = host.record.Name
			view.OwnerState, _ = host.status()
		}
		if last, ok := d.ledgerSync.last(key); ok {
			view.LastError = last.Error
			if !last.SyncedAt.IsZero() {
				synced := last.SyncedAt
				view.SyncedAt, view.OwnerHead = &synced, last.Head
				view.Lag = max(0, last.Head-status.Cursor)
			}
			switch check := last.Check; {
			case check.At.IsZero():
			case check.Error != "":
				view.LogError = check.Error
			case check.Match:
				view.Log, view.LogThrough = "match", check.Through
			default:
				view.Log, view.LogThrough, view.FirstDiff = "mismatch", check.Through, check.FirstDifference
			}
		}
		views = append(views, view)
	}
	return views, nil
}
