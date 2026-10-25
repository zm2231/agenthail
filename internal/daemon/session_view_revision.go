package daemon

import "time"

func revisionRows(rows []dashboardSession) []dashboardSession {
	out := make([]dashboardSession, len(rows))
	for i, row := range rows {
		row.ObservedAt = time.Time{}
		if row.Freshness != nil {
			freshness := *row.Freshness
			freshness.ObservedAt = time.Time{}
			row.Freshness = &freshness
		}
		out[i] = row
	}
	return out
}
