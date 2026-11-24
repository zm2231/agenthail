package daemon

import (
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// A discovery pass reads its rows' metadata a window of rows at a time and
// serves a row from the read while the presentation version shows a per-row
// read would return the same. A row whose window is stale starts a new window
// from it. Each publish that writes moves the version too, so the next window
// is as long as the rows a stale one served, and twice as long as one that
// served all its rows, up to catalogRowWindow: a pass whose rows keep changing
// reads one row at a time, as a per-row read would, and an unchanged one
// reads full windows.
const catalogRowWindow = 64

type catalogRowStore interface {
	CatalogRowMetadata([]string) (registry.CatalogRowMetadata, error)
	CatalogPresentationVersion() (uint64, error)
}

type catalogRowSource struct {
	sessions []string
	position map[string]int
	meta     registry.CatalogRowMetadata
	loaded   bool
	window   int
	served   int
}

func newCatalogRowSource(sessions []surface.Session) *catalogRowSource {
	source := &catalogRowSource{sessions: make([]string, len(sessions)), position: make(map[string]int, len(sessions)), window: catalogRowWindow}
	for index, session := range sessions {
		source.sessions[index] = session.ID
		source.position[session.ID] = index
	}
	return source
}

// row runs under the session's waiting lock, like the per-row reads it
// stands for. A nil source reads the one row.
func (s *catalogRowSource) row(store catalogRowStore, sessionID string) (registry.CatalogRow, error) {
	if s == nil {
		return readCatalogRow(store, sessionID)
	}
	position, listed := s.position[sessionID]
	if !listed {
		return readCatalogRow(store, sessionID)
	}
	if s.loaded {
		row, found := s.meta.Row(sessionID)
		if !found {
			s.window = min(2*s.served, catalogRowWindow)
		} else if version, err := store.CatalogPresentationVersion(); err != nil {
			return registry.CatalogRow{}, err
		} else if version == s.meta.Version {
			s.served++
			return row, nil
		} else {
			s.window = s.served
		}
	}
	meta, err := loadCatalogRows(store, s.sessions[position:min(position+s.window, len(s.sessions))])
	if err != nil {
		return registry.CatalogRow{}, err
	}
	s.meta, s.loaded, s.served = meta, true, 1
	row, _ := meta.Row(sessionID)
	return row, nil
}

func readCatalogRow(store catalogRowStore, sessionID string) (registry.CatalogRow, error) {
	meta, err := loadCatalogRows(store, []string{sessionID})
	if err != nil {
		return registry.CatalogRow{}, err
	}
	row, _ := meta.Row(sessionID)
	return row, nil
}

// loadCatalogRows reads again when a write moved the presentation version
// while the rows were read, so no row is served from a read that straddled a
// write; after a few tries the last read stands, as a per-row read would.
func loadCatalogRows(store catalogRowStore, sessions []string) (registry.CatalogRowMetadata, error) {
	var meta registry.CatalogRowMetadata
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		if meta, err = store.CatalogRowMetadata(sessions); err != nil {
			return registry.CatalogRowMetadata{}, err
		}
		version, err := store.CatalogPresentationVersion()
		if err != nil {
			return registry.CatalogRowMetadata{}, err
		}
		if version == meta.Version {
			break
		}
	}
	return meta, nil
}
