package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestARegistryAtSchema90MovesWaitsToAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, r)
	for _, statement := range []string{
		`DROP INDEX asks_ref`,
		`ALTER TABLE asks DROP COLUMN ref`,
		`ALTER TABLE asks DROP COLUMN wait_kind`,
		`ALTER TABLE asks DROP COLUMN answerable`,
		`ALTER TABLE asks DROP COLUMN announced_at_ms`,
		`ALTER TABLE session_runtime ADD COLUMN waiting_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_runtime ADD COLUMN waiting_answerable INTEGER NOT NULL DEFAULT 0`,
		`PRAGMA user_version = 90`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	r.Close()

	if current, err := OpenCurrent(path); err == nil {
		current.Close()
		t.Fatal("a hook opened a schema 90 registry that lacks the columns this build reads")
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, want := range []struct {
		table, column string
		found         int
	}{{"asks", "ref", 1}, {"asks", "answerable", 1}, {"asks", "announced_at_ms", 1}, {"session_runtime", "waiting_id", 0}, {"session_runtime", "waiting_answerable", 0}} {
		var found int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, want.table, want.column).Scan(&found); err != nil || found != want.found {
			t.Fatalf("%s.%s found=%d err=%v, want %d", want.table, want.column, found, err, want.found)
		}
	}
	register(t, r, "asking")
	if _, err := r.SyncWaits("asking", []Wait{{Ref: "req-1", Answerable: true}}, "", time.Now()); err != nil {
		t.Fatal(err)
	}
}
