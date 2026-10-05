package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWriterTransactionKeepsItsSnapshotWhileAnotherProcessWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		_, err := second.db.Exec(`INSERT INTO channels(id,name) VALUES('other','other')`)
		written <- err
	}()
	select {
	case err := <-written:
		t.Fatalf("another registry wrote inside an open read-then-write transaction: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := tx.Exec(`INSERT INTO channels(id,name) VALUES('mine','mine')`); err != nil {
		t.Fatalf("a transaction that read first could not write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatalf("the waiting writer failed instead of waiting: %v", err)
	}
}
