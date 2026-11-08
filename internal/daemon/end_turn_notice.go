package daemon

import (
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const AttentionAsk = "ask"

type dashboardEndTurn struct {
	Headline     string             `json:"headline"`
	Needs        string             `json:"needs"`
	Asks         []registry.TurnAsk `json:"asks,omitempty"`
	Next         []string           `json:"next,omitempty"`
	EndedAt      time.Time          `json:"endedAt"`
	AskedSession string             `json:"askedSession,omitempty"`
	RequestID    string             `json:"requestId,omitempty"`
	AnsweredAt   time.Time          `json:"answeredAt,omitzero"`
}

func endTurnRow(end *registry.TurnEnd) *dashboardEndTurn {
	if end == nil {
		return nil
	}
	row := &dashboardEndTurn{Headline: end.Headline, Needs: end.Needs, Asks: end.Asks, Next: end.Next, EndedAt: end.EndedAt, AskedSession: end.RoutedTo, AnsweredAt: end.AnsweredAt}
	if endAsksPerson(*end) || !end.AnsweredAt.IsZero() {
		row.RequestID = turnEndRequestID(end.StartedAt)
	}
	return row
}

func (end *dashboardEndTurn) asksPerson() bool {
	return end != nil && end.Needs == endTurnNeedsYou && end.AskedSession == "" && end.AnsweredAt.IsZero()
}

type turnCompletedNotice struct {
	desktop, mobile, requestID, outcome string
	actions                             []surface.RequestAction
}

// A needs-you an agent was told of keeps the plain finished text for the
// person; one for the person carries its ask, so the push can answer it.
func (d *Daemon) endTurnNotifications(session *surface.Session, completedTurnID, desktop, mobile string) turnCompletedNotice {
	notice := turnCompletedNotice{desktop: desktop, mobile: mobile, outcome: turnOutcomeFinished}
	end, found, err := d.Registry.CompletedTurnEnd(session.ID, completedTurnID)
	if err != nil {
		d.log.Printf("read turn end %s: %s", d.resolveDisplay(session.ID), err)
	}
	if !found || end.RoutedTo != "" {
		return notice
	}
	state := "done"
	if end.Needs == endTurnNeedsYou {
		state = "needs you"
	}
	notice.desktop = fmt.Sprintf("%s %s: %s", d.resolveDisplay(session.ID), state, end.Headline)
	notice.mobile = fmt.Sprintf("%s %s%s: %s", notificationSurfaceName(session.Surface), state, d.onHost(), end.Headline)
	if endAsksPerson(end) {
		request := turnEndRequest(end)
		notice.requestID, notice.actions, notice.outcome = request.ID, request.Actions(), turnOutcomeNeedsYou
	}
	return notice
}
