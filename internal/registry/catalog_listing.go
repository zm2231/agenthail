package registry

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/surface"
)

// CatalogListing is what a session list shows of the catalog. Every session
// that is not a subagent is listed. A subagent is listed only while it works
// or is current and its parent is listed, so it always sits under a listed
// row and never at the top level. Each hidden subagent counts toward its
// nearest listed ancestor's rollup.
type CatalogListing struct {
	Hidden      map[string]bool
	Rollups     map[string]surface.SubagentRollup
	descendants map[string]surface.SubagentRollup
}

type catalogListingRow struct {
	parent   string
	subagent bool
	active   bool
	busy     bool
}

func catalogListingTx(q catalogQuerier) (CatalogListing, error) {
	rows, err := q.Query(`SELECT s.id,s.parent_session_id,s.source,s.status,CASE WHEN json_valid(cs.projection_fingerprint) THEN COALESCE(json_extract(cs.projection_fingerprint,'$.current'),0) ELSE 0 END
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id`)
	if err != nil {
		return CatalogListing{}, err
	}
	byID := map[string]catalogListingRow{}
	for rows.Next() {
		var id, parent, source, status string
		var current bool
		if err := rows.Scan(&id, &parent, &source, &status, &current); err != nil {
			rows.Close()
			return CatalogListing{}, err
		}
		busy := status == string(surface.StatusBusy)
		byID[id] = catalogListingRow{parent: parent, subagent: parent != "" || source == "subAgent", active: busy || current, busy: busy}
	}
	if err := rows.Close(); err != nil {
		return CatalogListing{}, err
	}
	return catalogListing(byID), nil
}

func catalogListing(byID map[string]catalogListingRow) CatalogListing {
	listing := CatalogListing{Hidden: map[string]bool{}, Rollups: map[string]surface.SubagentRollup{}, descendants: map[string]surface.SubagentRollup{}}
	listed := map[string]bool{}
	var decide func(id string, path map[string]bool) bool
	decide = func(id string, path map[string]bool) bool {
		if shown, decided := listed[id]; decided {
			return shown
		}
		row := byID[id]
		shown := !row.subagent
		if row.subagent && row.active && row.parent != "" && !path[id] {
			if _, found := byID[row.parent]; found {
				path[id] = true
				shown = decide(row.parent, path)
			}
		}
		listed[id] = shown
		return shown
	}
	for id := range byID {
		if !decide(id, map[string]bool{}) {
			listing.Hidden[id] = true
		}
	}
	for id, row := range byID {
		seen := map[string]bool{id: true}
		for ancestor := row.parent; ancestor != "" && !seen[ancestor]; ancestor = byID[ancestor].parent {
			if _, found := byID[ancestor]; !found {
				break
			}
			seen[ancestor] = true
			rollup := listing.descendants[ancestor]
			rollup.Count++
			if row.busy {
				rollup.Working++
			}
			listing.descendants[ancestor] = rollup
		}
	}
	for id := range listing.Hidden {
		row := byID[id]
		seen := map[string]bool{id: true}
		for ancestor := row.parent; ancestor != "" && !seen[ancestor]; ancestor = byID[ancestor].parent {
			if _, found := byID[ancestor]; !found {
				break
			}
			seen[ancestor] = true
			if listed[ancestor] {
				rollup := listing.Rollups[ancestor]
				rollup.Count++
				if row.busy {
					rollup.Working++
				}
				listing.Rollups[ancestor] = rollup
				break
			}
		}
	}
	return listing
}

func (l CatalogListing) AddSubagents(id string, observed *surface.SubagentRollup) *surface.SubagentRollup {
	hidden, found := l.Rollups[id]
	if !found {
		return observed
	}
	if observed != nil {
		hidden.Count += observed.Count
		hidden.Working += observed.Working
	}
	return &hidden
}

func (l CatalogListing) hiddenJSON() string {
	ids := make([]string, 0, len(l.Hidden))
	for id := range l.Hidden {
		ids = append(ids, id)
	}
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}
