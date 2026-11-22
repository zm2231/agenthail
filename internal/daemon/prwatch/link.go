package prwatch

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// Link records a pull request against a session and logs it to the one work
// item it is for. That item is the one the link names, else the session's
// only item in progress; with neither, the pull request is logged nowhere.
// explicit is a link someone asked for, which turns the watch on and may name
// the item of a pull request already linked.
func (w *Watcher) Link(link registry.SessionPR, explicit bool) error {
	named := link.ItemID != ""
	link.ItemFrom = registry.PRItemNamed
	if !named {
		link.ItemID, link.ItemFrom = w.host.OnlyActiveItem(link.SessionID), registry.PRItemOnlyInProgress
	}
	if link.ItemID == "" {
		link.ItemFrom = ""
	}
	linked, err := w.host.ChangePRs(func(r *registry.Registry) (bool, error) { return r.LinkPR(link, explicit, time.Now()) })
	if err == nil && !linked && named && explicit {
		linked, err = w.host.ChangePRs(func(r *registry.Registry) (bool, error) {
			return r.SetPRItem(link.SessionID, link.Repo, link.Number, link.ItemID)
		})
	}
	if err == nil && linked && link.ItemID != "" {
		w.host.LogItem(link.SessionID, link.ItemID, "PR opened: "+link.URL)
	}
	return err
}

func outcomeLine(before, after registry.SessionPR) string {
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
