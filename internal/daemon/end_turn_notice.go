package daemon

import (
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const AttentionAsk = "ask"

type dashboardEndTurn struct {
	Headline string    `json:"headline"`
	Next     []string  `json:"next,omitempty"`
	EndedAt  time.Time `json:"endedAt"`
}

func endTurnRow(end *registry.TurnEnd) *dashboardEndTurn {
	if end == nil {
		return nil
	}
	return &dashboardEndTurn{Headline: end.Headline, Next: end.Next, EndedAt: end.EndedAt}
}

type dashboardAsk struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	RoutedTo   string    `json:"routedTo"`
	AskedAt    time.Time `json:"askedAt"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
}

func askRow(ask *registry.Ask) *dashboardAsk {
	if ask == nil {
		return nil
	}
	return &dashboardAsk{ID: ask.ID, Title: ask.Title, RoutedTo: ask.RoutedTo, AskedAt: ask.CreatedAt, AnsweredAt: ask.AnsweredAt}
}

func (ask *dashboardAsk) asksPerson() bool {
	return ask != nil && ask.RoutedTo == registry.AskToPerson && ask.AnsweredAt.IsZero()
}

type turnCompletedNotice struct {
	desktop, mobile, requestID, outcome string
	actions                             []surface.RequestAction
	quiet                               bool
}

// A turn that leaves an ask open for the person needs them; the ask was announced when it reached them, so the completion stays quiet.
func (d *Daemon) endTurnNotifications(session *surface.Session, completedTurnID, desktop, mobile string) turnCompletedNotice {
	notice := turnCompletedNotice{desktop: desktop, mobile: mobile, outcome: turnOutcomeFinished}
	ask, asked, err := d.Registry.RowAsk(session.ID)
	if err != nil {
		d.log.Printf("read the ask of %s: %s", d.resolveDisplay(session.ID), err)
	}
	if asked && ask.State == registry.AskOpen && ask.RoutedTo == registry.AskToPerson {
		notice.outcome, notice.requestID, notice.quiet = turnOutcomeNeedsYou, ask.ID, true
		return notice
	}
	end, found, err := d.Registry.CompletedTurnEnd(session.ID, completedTurnID)
	if err != nil {
		d.log.Printf("read turn end %s: %s", d.resolveDisplay(session.ID), err)
	}
	if found {
		notice.desktop = fmt.Sprintf("%s done: %s", d.resolveDisplay(session.ID), end.Headline)
		notice.mobile = fmt.Sprintf("%s done%s: %s", notificationSurfaceName(session.Surface), d.onHost(), end.Headline)
	}
	return notice
}
