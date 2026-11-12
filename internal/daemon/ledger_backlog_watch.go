package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	decisionParked       = "parked"
	ledgerAskStatesMost  = 100
	ledgerAskStatesEvery = 5 * time.Minute
)

type backlogWatch struct {
	mu     sync.Mutex
	ready  map[string][]string
	local  []usageSet
	remote []usageSet
	asks   map[string]map[string]string
	sent   map[string]sentAsks
}

type sentAsks struct {
	states map[string]string
	at     time.Time
}

func usageReaderOver(local, remote func() []usageSet, here string, now time.Time) ledger.UsageReader {
	return func(target ledger.ParkUsageTarget) ledger.Headroom {
		sets := remote
		if target.HostID == "" || target.HostID == here {
			sets = local
		}
		return parkHeadroom(sets(), target, now)
	}
}

func (d *Daemon) cachedLedgerUsage(now time.Time) ledger.UsageReader {
	d.backlog.mu.Lock()
	local, remote := d.backlog.local, d.backlog.remote
	d.backlog.mu.Unlock()
	return usageReaderOver(func() []usageSet { return local }, func() []usageSet { return remote }, d.localHost().ID, now)
}

func (d *Daemon) ledgerAskReader() ledger.AskReader {
	return func(q ledger.ParkQuestion) bool { return d.askState(q) == registry.AskAnswered }
}

func (d *Daemon) askState(q ledger.ParkQuestion) string {
	if q.AskID == "" {
		return ""
	}
	if q.HostID == "" || q.HostID == d.localHost().ID {
		ask, found, err := d.Registry.Ask(q.AskID)
		if err != nil || !found {
			return ""
		}
		return ask.State
	}
	d.backlog.mu.Lock()
	defer d.backlog.mu.Unlock()
	return d.backlog.asks[q.HostID][q.AskID]
}

// sendAskStates tells each remote ledger owner the state of the asks held here that its person parks name, since an owner may not reach this host.
func (d *Daemon) sendAskStates(ctx context.Context, store *ledger.Store, keys []string, now time.Time) {
	here := d.localHost().ID
	states := map[string]map[string]string{}
	for _, key := range keys {
		owner, err := store.RemoteOwner(key)
		if err != nil || owner == "" {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			continue
		}
		for _, entry := range board.Backlog(ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkPerson}}, time.Time{}, ledger.Readings{}) {
			q := *entry.Item.Park.Person
			if q.AskID == "" || q.HostID != here || len(states[owner]) >= ledgerAskStatesMost {
				continue
			}
			if states[owner] == nil {
				states[owner] = map[string]string{}
			}
			states[owner][q.AskID] = d.askState(ledger.ParkQuestion{AskID: q.AskID})
		}
	}
	for owner, held := range states {
		d.backlog.mu.Lock()
		last := d.backlog.sent[owner]
		d.backlog.mu.Unlock()
		sent := maps.Equal(last.states, held) && now.Sub(last.at) < ledgerAskStatesEvery
		host, ok := d.pairedHost(owner)
		if sent || !ok {
			continue
		}
		if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/ledger/sync/asks", nil, ledgerAskStates{States: held}, nil); err != nil {
			d.log.Printf("ledger backlog: asks to %s: %s", host.record.Name, err)
			continue
		}
		d.backlog.mu.Lock()
		if d.backlog.sent == nil {
			d.backlog.sent = map[string]sentAsks{}
		}
		d.backlog.sent[owner] = sentAsks{states: held, at: now}
		d.backlog.mu.Unlock()
	}
}

type ledgerAskStates struct {
	States map[string]string `json:"states"`
}

func (d *Daemon) apiLedgerSyncAsksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	hostID, ok := requestingHost(r.Context())
	if !ok || hostID == "" {
		writeAPIError(w, http.StatusForbidden, "host_only", "Only a paired host reports its asks.")
		return
	}
	var req ledgerAskStates
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, apiRequestBodyLimit)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if len(req.States) > ledgerAskStatesMost {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("report at most %d asks at a time.", ledgerAskStatesMost))
		return
	}
	d.backlog.mu.Lock()
	if d.backlog.asks == nil {
		d.backlog.asks = map[string]map[string]string{}
	}
	d.backlog.asks[hostID] = req.States
	d.backlog.mu.Unlock()
	d.decisions.poke()
	writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *Daemon) sweepLedgerBacklog(ctx context.Context, now time.Time) {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		d.log.Printf("ledger backlog: list projects: %s", err)
		return
	}
	var read struct{ local, remote bool }
	local := sync.OnceValue(func() []usageSet { read.local = true; return d.localUsageSets(ctx, now) })
	remote := sync.OnceValue(func() []usageSet { read.remote = true; return d.remoteUsageSets(ctx) })
	d.sendAskStates(ctx, store, keys, time.Now())
	readings := ledger.Readings{Usage: usageReaderOver(local, remote, d.localHost().ID, now), Asks: d.ledgerAskReader()}
	ready := map[string][]string{}
	for _, key := range keys {
		board, err := store.Board(key)
		if err != nil {
			d.log.Printf("ledger backlog: board of %s: %s", key, err)
			continue
		}
		ids := []string{}
		for _, entry := range board.Backlog(ledger.BacklogQuery{Ready: true}, now, readings) {
			ids = append(ids, entry.Item.ID)
		}
		ready[key] = ids
	}
	d.backlog.mu.Lock()
	if read.local {
		d.backlog.local = local()
	}
	if read.remote {
		d.backlog.remote = remote()
	}
	previous := d.backlog.ready
	d.backlog.ready = ready
	d.backlog.mu.Unlock()
	for key, ids := range ready {
		if was, seen := previous[key]; !seen || !slices.Equal(was, ids) {
			d.ledgerChanged(key)
		}
		if err := d.Registry.KeepBacklogTold(key, ids); err != nil {
			d.log.Printf("ledger backlog: told marks of %s: %s", key, err)
		}
	}
}

func (d *Daemon) backlogReadyLine(ctx context.Context, caller *mcpCaller, now time.Time) string {
	spec, coordinates, err := d.Registry.CoordinatorOfSession(caller.session.ID)
	if err != nil || !coordinates || spec.ProjectRoot == "" {
		return ""
	}
	key, err := ResolvePathLedgerKey(ctx, d.Registry, spec.ProjectRoot)
	if err != nil || key == "" {
		return ""
	}
	board, err := d.ledgerStore().Board(key)
	if err != nil {
		return ""
	}
	told, err := d.Registry.BacklogTold(key)
	if err != nil {
		return ""
	}
	var parts []string
	var fresh []string
	for _, entry := range board.Backlog(ledger.BacklogQuery{Ready: true}, now, ledger.Readings{Usage: d.cachedLedgerUsage(now), Asks: d.ledgerAskReader()}) {
		if told[entry.Item.ID] {
			continue
		}
		part := fmt.Sprintf("%s ready (%s: %s)", entry.Item.ID, entry.Item.Park.Kind, entry.Why)
		if entry.Item.Park.Session != "" {
			part += ", parked by " + entry.Item.Park.Session
		}
		parts, fresh = append(parts, part), append(fresh, entry.Item.ID)
	}
	if len(fresh) == 0 || d.Registry.MarkBacklogTold(key, fresh, now) != nil {
		return ""
	}
	return "Backlog: " + strings.Join(parts, "; ") + "."
}

func (d *Daemon) parkedDecisions(rows []dashboardSession, now time.Time) []decisionItem {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		d.log.Printf("decisions: ledger projects: %s", err)
		return nil
	}
	projects := map[string]string{}
	for _, row := range rows {
		projects[row.ID] = projectIDOf(row)
	}
	asks := d.ledgerAskReader()
	items := []decisionItem{}
	for _, key := range keys {
		if owner, err := store.RemoteOwner(key); err != nil || owner != "" {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			continue
		}
		for _, entry := range board.Backlog(ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkPerson}}, now, ledger.Readings{Asks: asks}) {
			question := entry.Item.Park.Person
			if entry.Ready || d.askState(*question) == registry.AskOpen {
				continue
			}
			item := decisionItem{Group: decisionWaiting, Kind: decisionParked, WorkItem: entry.Item.ID, Reason: entry.Item.ID + " waits on you: " + question.Question}
			if session := entry.Item.Park.Session; session != "" {
				item.SessionID = d.publicSessionID(session)
				item.ProjectID = projects[item.SessionID]
			}
			if timeline := entry.Item.Timeline; len(timeline) > 0 {
				item.Since = timeline[len(timeline)-1].At
			}
			items = append(items, item)
		}
	}
	return items
}

func (d *Daemon) parkedDecisionsNow(rows []dashboardSession) []decisionItem {
	return d.parkedDecisions(rows, time.Now())
}
