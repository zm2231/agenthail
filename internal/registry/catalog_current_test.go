package registry

import (
	"path/filepath"
	"testing"
)

func catalogCurrent(t *testing.T, r *Registry, id string) (bool, string) {
	t.Helper()
	var current bool
	var reason string
	if err := r.db.QueryRow(`SELECT current,current_reason FROM catalog_sessions WHERE session_id=?`, id).Scan(&current, &reason); err != nil {
		t.Fatal(err)
	}
	return current, reason
}

func TestCatalogCurrentFollowsEveryWriteOfTheProjection(t *testing.T) {
	r := openTestRegistry(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := r.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	want := func(step string, current bool, reason string) {
		t.Helper()
		if gotCurrent, gotReason := catalogCurrent(t, r, "s"); gotCurrent != current || gotReason != reason {
			t.Fatalf("%s: current=%v reason=%q, want %v %q", step, gotCurrent, gotReason, current, reason)
		}
	}
	exec(`INSERT INTO sessions(id,surface,name) VALUES('s','claude','s')`)
	exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,observed_at,projection_fingerprint) VALUES('s','{}','{}','now','{"current":true,"currentReason":"working"}')`)
	want("insert", true, "working")
	exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,observed_at,projection_fingerprint) VALUES('s','{}','{}','now','{"current":true,"currentReason":"queued"}')
		ON CONFLICT(session_id) DO UPDATE SET projection_fingerprint=excluded.projection_fingerprint`)
	want("upsert", true, "queued")
	exec(`UPDATE catalog_sessions SET projection_fingerprint='{"current":false}' WHERE session_id='s'`)
	want("update", false, "")
	exec(`UPDATE catalog_sessions SET projection_fingerprint='{"current":true,"currentReason":"recent"}' WHERE session_id='s'`)
	exec(`UPDATE catalog_sessions SET misses=misses+1 WHERE session_id='s'`)
	want("an update that leaves the projection alone", true, "recent")
	exec(`UPDATE catalog_sessions SET projection_fingerprint='' WHERE session_id='s'`)
	want("a cleared projection", false, "")
}

func TestOpeningAnOlderRegistryFillsCatalogCurrentFromTheProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO sessions(id,surface,name) VALUES('working','claude','working'),('idle','claude','idle')`,
		`DROP TRIGGER catalog_sessions_current_insert`,
		`INSERT INTO catalog_sessions(session_id,host_project,checkout,observed_at,projection_fingerprint) VALUES('working','{}','{}','now','{"current":true,"currentReason":"working"}'),('idle','{}','{}','now','{"current":false}')`,
		`PRAGMA user_version=28`,
	} {
		if _, err := r.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if current, _ := catalogCurrent(t, r, "working"); current {
		t.Fatal("the fixture already had current set")
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if current, reason := catalogCurrent(t, r, "working"); !current || reason != "working" {
		t.Fatalf("working: current=%v reason=%q", current, reason)
	}
	if current, reason := catalogCurrent(t, r, "idle"); current || reason != "" {
		t.Fatalf("idle: current=%v reason=%q", current, reason)
	}
}
