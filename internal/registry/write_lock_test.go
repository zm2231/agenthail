package registry

import (
	"database/sql"
	"fmt"
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

func TestIdleQueueScansTakeNoWriteLock(t *testing.T) {
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
	held, err := second.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	done := make(chan error, 1)
	go func() {
		now := time.Now()
		if _, err := first.ExpireMessages(now); err != nil {
			done <- err
			return
		}
		if err := first.ReconcileAttentionItems(now); err != nil {
			done <- err
			return
		}
		_, err := first.ClaimNextMessage("idle-session", now)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an idle expiry, attention or claim scan waited for another process's write lock")
	}
}

func TestClaimsWithNothingClaimableTakeNoWriteLock(t *testing.T) {
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
	register(t, first, "backoff")
	register(t, first, "queued")
	now := time.Unix(100, 0)
	if _, err := first.QueueMessageWithKey("backoff", "retry later", "backoff-1"); err != nil {
		t.Fatal(err)
	}
	item, err := first.ClaimNextMessage("backoff", now)
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := first.NackMessage(item.ID, sql.ErrConnDone, now, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := first.QueueMessageWithKey("queued", "not a steer", "queued-1"); err != nil {
		t.Fatal(err)
	}
	held, err := second.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	done := make(chan error, 1)
	go func() {
		if item, err := first.ClaimNextMessage("backoff", now); err != nil || item != nil {
			done <- fmt.Errorf("claim during backoff item=%+v err=%v", item, err)
			return
		}
		if item, err := first.ClaimNextSteerMessage("queued", now); err != nil || item != nil {
			done <- fmt.Errorf("steer claim of a queued message item=%+v err=%v", item, err)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a claim with nothing claimable waited for another process's write lock")
	}
}
