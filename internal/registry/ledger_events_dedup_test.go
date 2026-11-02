package registry

import (
	"path/filepath"
	"testing"
)

func TestLedgerEventsGainMessageIDDedupFromBeforeTheyHadOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO ledger_events(project_key,type,payload) VALUES('p','utterance','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`DROP INDEX ledger_events_dedup; ALTER TABLE ledger_events DROP COLUMN message_id; PRAGMA user_version=41`); err != nil {
		t.Fatal(err)
	}
	r.Close()

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var existing string
	if err := r.db.QueryRow(`SELECT message_id FROM ledger_events WHERE project_key='p'`).Scan(&existing); err != nil || existing != "" {
		t.Fatalf("pre-upgrade row message_id = %q, %v", existing, err)
	}
	insert := `INSERT INTO ledger_events(project_key,type,payload,message_id) VALUES('p','utterance','{}','u-1')`
	if _, err := r.db.Exec(insert); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(insert); err == nil {
		t.Fatal("two ledger events share a project and message id after upgrade")
	}
	if _, err := r.db.Exec(`INSERT INTO ledger_events(project_key,type,payload) VALUES('p','goal','{}')`); err != nil {
		t.Fatalf("events without a message id must not collide: %v", err)
	}
}
