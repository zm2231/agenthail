package daemon

import (
	"context"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type prHost struct{ d *Daemon }

func (h prHost) GitHub() github.Client { return h.d.github }

func (h prHost) ChangePRs(write func(*registry.Registry) (bool, error)) (bool, error) {
	return h.d.changePRs(write)
}

func (h prHost) RowsChanged() { h.d.requestCatalogDiscovery() }

func (h prHost) Display(sessionID string) string { return h.d.resolveDisplay(sessionID) }

func (h prHost) SettleWork(sessionID, reason string) { h.d.settleWork(sessionID, reason) }

func (h prHost) LatestTimeline(ctx context.Context, session *surface.Session, adapter surface.Surface, timeout time.Duration) ([]surface.TimelineItem, error) {
	read, _, err := h.d.readSessionPage(ctx, session, adapter, 0, 1, timeout, true)
	return read.Items, err
}

func (h prHost) TimelineText(sessionID string, item surface.TimelineItem) string {
	if !item.Truncated || item.BodyRef == "" {
		return item.Text
	}
	body, _, err := h.d.Registry.SessionJournalBody(sessionID, item.BodyRef, 0, sessionJournalRetentionBytes)
	if err != nil {
		return item.Text
	}
	return string(body)
}

func (d *Daemon) changePRs(write func(*registry.Registry) (bool, error)) (bool, error) {
	changed, err := write(d.Registry)
	if err == nil && changed {
		d.requestCatalogDiscovery()
	}
	return changed, err
}

func (h prHost) LogHeldWork(sessionID, line string) {
	d := h.d
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return
	}
	held, err := d.heldWork(*session)
	if err != nil {
		d.log.Printf("held work for %s: %s", d.resolveDisplay(sessionID), err)
		return
	}
	for _, h := range held {
		if h.projectID == "" || slices.ContainsFunc(h.item.Timeline, func(e ledger.WorkEntry) bool { return e.Text == line && e.Source == ledger.WorkSourcePRWatcher }) {
			continue
		}
		req := ledger.WorkRequest{ProjectKey: h.key, SessionID: sessionID, Action: ledger.WorkLog, ItemID: h.item.ID, Text: line, LogType: "progress", Source: ledger.WorkSourcePRWatcher}
		if _, err := d.ledgerStore().Work(req, time.Now()); err != nil {
			d.log.Printf("log %q to %s: %s", line, h.item.ID, err)
		}
	}
}
