package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

const decisionParked = "parked"

type backlogWatch struct {
	mu     sync.Mutex
	ready  map[string][]string
	local  []usageSet
	remote []usageSet
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
	return func(id string) bool {
		ask, found, err := d.Registry.Ask(id)
		return err == nil && found && ask.State == registry.AskAnswered
	}
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

func (d *Daemon) backlogReadyLine(caller *mcpCaller, now time.Time) string {
	if _, coordinates, err := d.Registry.CoordinatorOfSession(caller.session.ID); err != nil || !coordinates {
		return ""
	}
	key, err := LedgerKey(d.Registry, caller.project)
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
			if entry.Ready || question.AskID != "" && d.askOpen(question.AskID) {
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

func (d *Daemon) askOpen(id string) bool {
	ask, found, err := d.Registry.Ask(id)
	return err == nil && found && ask.State == registry.AskOpen
}

func (d *Daemon) parkedDecisionsNow(rows []dashboardSession) []decisionItem {
	return d.parkedDecisions(rows, time.Now())
}
