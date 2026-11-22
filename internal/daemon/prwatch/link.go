package prwatch

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// Link records a pull request against a session and logs it to the work the
// session holds when it is new. explicit is a link someone asked for, which
// turns the watch on.
func (w *Watcher) Link(link registry.SessionPR, explicit bool) error {
	linked, err := w.host.ChangePRs(func(r *registry.Registry) (bool, error) { return r.LinkPR(link, explicit, time.Now()) })
	if err == nil && linked {
		w.host.LogHeldWork(link.SessionID, "PR opened: "+link.URL)
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
