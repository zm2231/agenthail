package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func recordPresenceRow(t *testing.T, r *Registry, id, projection string) {
	t.Helper()
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: id, Cwd: "/work"}, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: id + ":" + projection, Type: "session.upserted", EntityID: id, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
}

func presenceText(read CatalogPresenceRead) map[string]string {
	texts := map[string]string{}
	for _, row := range read.Rows {
		if row.FingerprintRead {
			texts[row.Session.ID] = row.ProjectionFingerprint
		}
	}
	return texts
}

func TestCatalogPresenceRowsReadOnlyTheProjectionsWrittenSinceTheClock(t *testing.T) {
	r := openTestRegistry(t)
	recordPresenceRow(t, r, "first", `{"id":"first"}`)
	recordPresenceRow(t, r, "second", `{"id":"second"}`)
	nothingKnown := func(string, int64) bool { return false }
	full, err := r.CatalogPresenceRows(nil, "", 0, nothingKnown)
	if err != nil || len(full.Rows) != 2 || len(presenceText(full)) != 2 || full.HostEpoch == "" || full.Clock == 0 {
		t.Fatalf("first read=%+v err=%v", full, err)
	}
	generations := map[string]int64{}
	for _, row := range full.Rows {
		generations[row.Session.ID] = row.Generation
	}
	known := func(id string, generation int64) bool { return generations[id] == generation }
	quiet, err := r.CatalogPresenceRows(nil, full.HostEpoch, full.Clock, known)
	if err != nil || len(quiet.Rows) != 2 || len(presenceText(quiet)) != 0 || quiet.Clock != full.Clock {
		t.Fatalf("unchanged read=%+v err=%v", quiet, err)
	}
	if _, _, err := r.UpdateCatalogSessionProjection("second", `{"id":"second"}`, `{"id":"second","name":"renamed"}`); err != nil {
		t.Fatal(err)
	}
	changed, err := r.CatalogPresenceRows(nil, full.HostEpoch, full.Clock, known)
	if texts := presenceText(changed); err != nil || len(texts) != 1 || texts["second"] != `{"id":"second","name":"renamed"}` || changed.Clock <= full.Clock {
		t.Fatalf("changed read=%+v err=%v", changed, err)
	}
	for _, row := range changed.Rows {
		generations[row.Session.ID] = row.Generation
	}
	if other, err := r.CatalogPresenceRows(nil, "another-epoch", changed.Clock, known); err != nil || len(presenceText(other)) != 2 {
		t.Fatalf("a read from another epoch=%+v err=%v, want every projection", other, err)
	}
	forgotten := func(id string, generation int64) bool { return id != "first" && known(id, generation) }
	if missing, err := r.CatalogPresenceRows(nil, changed.HostEpoch, changed.Clock, forgotten); err != nil || len(presenceText(missing)) != 1 || presenceText(missing)["first"] != `{"id":"first"}` {
		t.Fatalf("a read missing a row=%+v err=%v, want that row's projection", missing, err)
	}
	if _, err := r.db.Exec(`DELETE FROM catalog_state`); err != nil {
		t.Fatal(err)
	}
	if stateless, err := r.CatalogPresenceRows(nil, changed.HostEpoch, changed.Clock, known); err != nil || len(presenceText(stateless)) != 2 {
		t.Fatalf("a read with no catalog state=%+v err=%v, want every projection", stateless, err)
	}
}
