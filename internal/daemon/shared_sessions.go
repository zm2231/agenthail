package daemon

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type dashboardSharedSession struct {
	ID        string                `json:"id"`
	Name      string                `json:"name,omitempty"`
	PID       int                   `json:"pid"`
	Status    surface.SessionStatus `json:"status"`
	StartedAt time.Time             `json:"startedAt,omitzero"`
}

// sharedClaudeSessions groups live Claude rows by transcript and returns, for
// each row in a group of two or more, the other rows of its group ordered by
// process start.
func sharedClaudeSessions(sessions []surface.Session) map[string][]dashboardSharedSession {
	groups := map[string][]surface.Session{}
	for _, session := range sessions {
		if session.Surface != surface.KindClaude || session.Transcript == "" || session.PID <= 0 {
			continue
		}
		groups[session.Transcript] = append(groups[session.Transcript], session)
	}
	shared := map[string][]dashboardSharedSession{}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		slices.SortFunc(group, func(left, right surface.Session) int {
			if order := left.StartedAt.Compare(right.StartedAt); order != 0 {
				return order
			}
			return strings.Compare(left.ID, right.ID)
		})
		for _, session := range group {
			others := make([]dashboardSharedSession, 0, len(group)-1)
			for _, other := range group {
				if other.ID == session.ID {
					continue
				}
				others = append(others, dashboardSharedSession{ID: other.ID, Name: other.Name, PID: other.PID, Status: other.Status, StartedAt: other.StartedAt})
			}
			shared[session.ID] = others
		}
	}
	return shared
}

// savedSharedSessions reads the relation from the session's committed catalog
// projection, the row snapshots serve, so detail and snapshots always agree.
func (d *Daemon) savedSharedSessions(sessionID string) ([]dashboardSharedSession, error) {
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	for _, record := range snapshot.Sessions {
		if record.Session.ID != sessionID {
			continue
		}
		var saved dashboardSession
		if err := json.Unmarshal([]byte(record.ProjectionFingerprint), &saved); err != nil {
			return nil, err
		}
		return saved.SharedWith, nil
	}
	return nil, nil
}

// refreshCatalogShared republishes live Claude rows whose shared-process list
// no longer matches the rows that committed, so a relation only names peers
// clients can open and each entry carries its peer's current status. A failed
// republish is retried on the next status tick.
func (d *Daemon) refreshCatalogShared(config *DashboardConfig, counts map[string]int) {
	sessions := make([]surface.Session, 0, len(d.catalogLive))
	for _, live := range d.catalogLive {
		if live.adapter.Name() == surface.KindClaude {
			sessions = append(sessions, live.session)
		}
	}
	shared := sharedClaudeSessions(sessions)
	for id, live := range d.catalogLive {
		if live.adapter.Name() != surface.KindClaude || equalSharedSessions(live.shared, shared[id]) {
			continue
		}
		if config == nil {
			loaded, err := LoadDashboardConfig()
			if err != nil {
				d.log.Printf("catalog config: %s", err)
				return
			}
			config = &loaded
			if counts, err = d.Registry.QueueCounts(); err != nil {
				d.log.Printf("catalog queue counts: %s", err)
				return
			}
		}
		if d.publishCatalogSession(live.adapter, live.session, live.identity, live.alias, counts[id], live.open, shared[id], *config, time.Now().UTC()) {
			live.shared = shared[id]
		}
	}
}

func equalSharedSessions(left, right []dashboardSharedSession) bool {
	return slices.EqualFunc(left, right, func(a, b dashboardSharedSession) bool {
		return a.ID == b.ID && a.Name == b.Name && a.PID == b.PID && a.Status == b.Status && a.StartedAt.Equal(b.StartedAt)
	})
}
