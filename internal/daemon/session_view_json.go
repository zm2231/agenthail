package daemon

import (
	"encoding/json"
	"time"
)

type sessionViewFreshness struct {
	Generation uint64 `json:"generation"`
	Stale      bool   `json:"stale"`
}

type sessionViewRow struct {
	dashboardSession
	ObservedAt *time.Time            `json:"observedAt,omitempty"`
	Freshness  *sessionViewFreshness `json:"freshness,omitempty"`
}

func (s SessionViewSnapshot) MarshalJSON() ([]byte, error) {
	type plain SessionViewSnapshot
	rows := make([]sessionViewRow, len(s.Sessions))
	for i, session := range s.Sessions {
		rows[i] = sessionViewRow{dashboardSession: session}
		if session.Freshness != nil {
			rows[i].Freshness = &sessionViewFreshness{Generation: session.Freshness.Generation, Stale: session.Freshness.Stale}
		}
	}
	return json.Marshal(struct {
		plain
		Sessions []sessionViewRow `json:"sessions"`
	}{plain(s), rows})
}
