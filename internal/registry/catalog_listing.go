package registry

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

// CatalogListing is what a session list shows of the catalog. Every session
// that is not a subagent is listed. A subagent is listed only while it works
// or is current and its parent is listed, so it always sits under a listed
// row and never at the top level. Each hidden subagent counts toward its
// nearest listed ancestor's rollup.
//
// Retired rows are listed but belong to history rather than the current
// list: an offline session that nothing makes current, and every listed row
// beneath one. Pages show them; the current snapshot and the device stream
// do not.
type CatalogListing struct {
	Hidden          map[string]bool
	Retired         map[string]bool
	Rollups         map[string]surface.SubagentRollup
	descendants     map[string]surface.SubagentRollup
	hiddenEntities  map[string]bool
	retiredEntities map[string]bool
}

type catalogListingRow struct {
	entity   string
	parent   string
	subagent bool
	active   bool
	busy     bool
	retired  bool
}

func catalogListingTx(q catalogQuerier) (CatalogListing, error) {
	rows, err := q.Query(`SELECT s.id,s.host_id,s.parent_session_id,s.source,s.status,CASE WHEN json_valid(cs.projection_fingerprint) THEN COALESCE(json_extract(cs.projection_fingerprint,'$.current'),0) ELSE 0 END
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id`)
	if err != nil {
		return CatalogListing{}, err
	}
	byID := map[string]catalogListingRow{}
	for rows.Next() {
		var id, hostID, parent, source, status string
		var current bool
		if err := rows.Scan(&id, &hostID, &parent, &source, &status, &current); err != nil {
			rows.Close()
			return CatalogListing{}, err
		}
		busy := status == string(surface.StatusBusy)
		retired := status == string(surface.StatusOffline) && !current
		byID[id] = catalogListingRow{entity: hosts.Qualify(hostID, id), parent: parent, subagent: delegatedColumns(parent, source), active: busy || current, busy: busy, retired: retired}
	}
	if err := rows.Close(); err != nil {
		return CatalogListing{}, err
	}
	return catalogListing(byID), nil
}

func catalogListing(byID map[string]catalogListingRow) CatalogListing {
	listing := CatalogListing{Hidden: map[string]bool{}, Retired: map[string]bool{}, Rollups: map[string]surface.SubagentRollup{}, descendants: map[string]surface.SubagentRollup{}, hiddenEntities: map[string]bool{}, retiredEntities: map[string]bool{}}
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
			listing.hiddenEntities[byID[id].entity] = true
		}
	}
	for id, row := range byID {
		if !listed[id] {
			continue
		}
		seen := map[string]bool{}
		for at := id; at != "" && !seen[at]; at = byID[at].parent {
			above, found := byID[at]
			if !found {
				break
			}
			seen[at] = true
			if above.retired {
				listing.Retired[id] = true
				listing.retiredEntities[row.entity] = true
				break
			}
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

func (l CatalogListing) HiddenSubagents(id string) *surface.SubagentRollup {
	rollup, found := l.Rollups[id]
	if !found {
		return nil
	}
	return &rollup
}

// Current reports whether a row belongs to the current session list.
func (l CatalogListing) Current(id string) bool {
	return !l.Hidden[id] && !l.Retired[id]
}

// CurrentEntity reports whether a host-qualified row belongs to the current
// session list.
func (l CatalogListing) CurrentEntity(entityID string) bool {
	return !l.hiddenEntities[entityID] && !l.retiredEntities[entityID]
}

func (r *Registry) CatalogListing() (CatalogListing, error) {
	return catalogListingTx(r.read)
}

func (l CatalogListing) hiddenJSON() string {
	ids := make([]string, 0, len(l.Hidden))
	for id := range l.Hidden {
		ids = append(ids, id)
	}
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}

func delegatedColumns(parent, source string) bool {
	session := surface.Session{Source: source}
	if parent != "" {
		session.Subagent = &surface.Subagent{ParentID: parent}
	}
	return session.Delegated()
}
