package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// syncQuestions makes the session's question asks follow what its provider's
// history shows: a new question raises an ask routed like an agent's own, and
// a question answered or superseded in the provider's history withdraws its
// ask.
func (d *Daemon) syncQuestions(session surface.Session, found surface.ProviderQuestions) {
	if len(found.Open)+len(found.Answered) == 0 && !found.Superseded {
		return
	}
	routedTo, hearer, routed := "", noticeRecipient{}, false
	if len(found.Open) > 0 {
		needs, err := d.Registry.QuestionAskNeedsRoute(session.ID, found.Open)
		if err != nil {
			d.log.Printf("route the questions of %s: %s", d.resolveDisplay(session.ID), err)
		} else if needs {
			routedTo = registry.AskToPerson
			if hearer, routed = d.askHearer(session.ID); routed {
				routedTo = hearer.sessionID
			}
		}
	}
	sync, err := d.Registry.SyncQuestionAsks(session.ID, found, routedTo, time.Now())
	if err != nil {
		d.log.Printf("record the questions of %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	for _, ask := range sync.Closed {
		if ask.RoutedTo != registry.AskToPerson {
			if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
				d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
			}
		}
	}
	if len(sync.Closed) > 0 {
		d.sendWithdraws()
	}
	for _, ask := range sync.Opened {
		if routed {
			d.tellHearer(ask, hearer)
		} else {
			d.announceAsk(ask)
		}
	}
	if len(sync.Opened)+len(sync.Closed) > 0 {
		d.publishAsk(session.ID)
	}
}
