package ledger

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

func TestAMoveFromAnotherWriterWaitsForAnOpenLedgerWrite(t *testing.T) {
	path := registrytest.Path(t)
	writer, mover := openRegistry(t, path), openRegistry(t, path)
	const before, after = "repo:github.com/acme/widget", "repo:github.com/acme/renamed-widget"
	if _, err := mover.ObserveRepositoryKey("/src/widget", before); err != nil {
		t.Fatal(err)
	}
	s := NewStore(writer.WriteDB(), writer.ReadDB())
	tx, err := s.begin(before)
	if err != nil {
		t.Fatal(err)
	}
	moved := make(chan error, 1)
	go func() {
		_, err := mover.ObserveRepositoryKey("/src/widget", after)
		moved <- err
	}()
	select {
	case err := <-moved:
		tx.Rollback()
		t.Fatalf("a move committed between a ledger write's moved check and its write (err %v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := tx.Exec(`INSERT INTO ledger_events(project_key,type,payload) VALUES(?,?,'{}')`, before, string(EventGoal)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
	if held, err := writer.HoldsLedger(before); err != nil || held {
		t.Fatalf("the write landed under the old key after the move: %t %v", held, err)
	}
	if held, err := writer.HoldsLedger(after); err != nil || !held {
		t.Fatalf("the write did not move with the project: %t %v", held, err)
	}
}
