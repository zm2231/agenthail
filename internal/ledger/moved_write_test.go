package ledger

import (
	"database/sql"
	"testing"

	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

func TestNoOtherWriterCanMoveAProjectUnderAnOpenLedgerWrite(t *testing.T) {
	path := registrytest.Path(t)
	writer, mover := openRegistry(t, path), openRegistry(t, path)
	const before, after = "repo:github.com/acme/widget", "repo:github.com/acme/renamed-widget"
	if _, err := mover.ObserveRepositoryKey("/src/widget", before); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)

	s := NewStore(writer.WriteDB(), writer.ReadDB())
	tx, err := s.begin(before)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if contender, err := other.Begin(); err == nil {
		contender.Rollback()
		t.Fatal("another writer started a write while a ledger write was open past its moved check")
	}
	if _, err := tx.Exec(`INSERT INTO ledger_events(project_key,type,payload) VALUES(?,?,'{}')`, before, string(EventGoal)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := mover.ObserveRepositoryKey("/src/widget", after); err != nil {
		t.Fatal(err)
	}
	if held, err := writer.HoldsLedger(before); err != nil || held {
		t.Fatalf("the write stayed under the old key after the move: %t %v", held, err)
	}
	if held, err := writer.HoldsLedger(after); err != nil || !held {
		t.Fatalf("the write did not move with the project: %t %v", held, err)
	}
}
