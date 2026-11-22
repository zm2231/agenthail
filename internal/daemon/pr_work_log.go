package daemon

import (
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) linkPR(link registry.SessionPR, explicit bool) error {
	linked, err := d.changePRs(func(r *registry.Registry) (bool, error) { return r.LinkPR(link, explicit, time.Now()) })
	if err == nil && linked {
		d.logToHeldWork(link.SessionID, "PR opened: "+link.URL)
	}
	return err
}

func prOutcomeLine(before, after registry.SessionPR) string {
	switch {
	case before.State == after.State:
		return ""
	case after.State == "merged":
		return "PR merged: " + after.URL
	case after.State == "closed":
		return "PR closed unmerged: " + after.URL
	}
	return ""
}

func (d *Daemon) logToHeldWork(sessionID, line string) {
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
		if h.projectID == "" || slices.ContainsFunc(h.item.Timeline, func(e ledger.WorkEntry) bool { return e.Text == line }) {
			continue
		}
		req := ledger.WorkRequest{ProjectKey: h.key, SessionID: sessionID, Action: ledger.WorkLog, ItemID: h.item.ID, Text: line, LogType: "progress"}
		if _, err := d.ledgerStore().Work(req, time.Now()); err != nil {
			d.log.Printf("log %q to %s: %s", line, h.item.ID, err)
		}
	}
}
