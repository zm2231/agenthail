package registry

import (
	"bytes"
	"encoding/json"
	"slices"
)

// catalogChangedKey names the session.upserted envelope field listing the
// top-level projection keys that differ from the row the catalog held before.
// An upsert without it may have changed anything: the row is new, was stale,
// or moved project, checkout, availability or retirement as well.
const catalogChangedKey = "changed"

func projectionChanges(prior, next string) (changed []string, ok bool) {
	var before, after map[string]json.RawMessage
	if json.Unmarshal([]byte(prior), &before) != nil || json.Unmarshal([]byte(next), &after) != nil || before == nil || after == nil {
		return nil, false
	}
	changed = []string{}
	for key, value := range after {
		if old, found := before[key]; !found || !bytes.Equal(old, value) {
			changed = append(changed, key)
		}
	}
	for key := range before {
		if _, found := after[key]; !found {
			changed = append(changed, key)
		}
	}
	slices.Sort(changed)
	return changed, true
}

func (p catalogSessionPrior) changesFrom(fingerprint string, project, checkout json.RawMessage, unavailable string) ([]string, bool) {
	if !p.found || p.misses > 0 || p.failures > 0 || p.retired || !bytes.Equal(p.project, project) || !bytes.Equal(p.checkout, checkout) || p.unavailable != unavailable {
		return nil, false
	}
	return projectionChanges(p.fingerprint, fingerprint)
}

func SessionUpsertChanges(event CatalogEvent) (changed []string, known bool) {
	if event.Type != "session.upserted" {
		return nil, false
	}
	var envelope struct {
		Changed *[]string `json:"changed"`
	}
	if json.Unmarshal(event.Payload, &envelope) != nil || envelope.Changed == nil {
		return nil, false
	}
	return *envelope.Changed, true
}
