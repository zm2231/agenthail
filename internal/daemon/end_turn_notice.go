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
}

func endTurnRow(end *registry.TurnEnd) *dashboardEndTurn {
	if end == nil {
		return nil
	}
	return &dashboardEndTurn{Headline: end.Headline, Needs: end.Needs, Asks: end.Asks, Next: end.Next, EndedAt: end.EndedAt, AskedSession: end.RoutedTo}
}

func (end *dashboardEndTurn) asksPerson() bool {
	return end != nil && end.Needs == endTurnNeedsYou && end.AskedSession == ""
}

// A needs-you an agent was told of keeps the plain finished text for the person.
func (d *Daemon) endTurnNotifications(session *surface.Session, completedTurnID, desktop, mobile string) (string, string) {
	end, found, err := d.Registry.CompletedTurnEnd(session.ID, completedTurnID)
	if err != nil {
		d.log.Printf("read turn end %s: %s", d.resolveDisplay(session.ID), err)
	}
	if !found || end.RoutedTo != "" {
		return desktop, mobile
	}
	state := "done"
	if end.Needs == endTurnNeedsYou {
		state = "needs you"
	}
	return fmt.Sprintf("%s %s: %s", d.resolveDisplay(session.ID), state, end.Headline), fmt.Sprintf("%s %s: %s", notificationSurfaceName(session.Surface), state, end.Headline)
}
