package registry

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func lockRegistry(t *testing.T, path string) (unlock func()) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var done bool
	unlock = func() {
		if done {
			return
		}
		done = true
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		conn.Close()
		db.Close()
	}
	t.Cleanup(unlock)
	return unlock
}

func busyTimeout(t *testing.T, r *Registry) int {
	t.Helper()
	var ms int
	if err := r.db.raw.QueryRow(`PRAGMA busy_timeout`).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestABoundWriteWaitsForTheLockUntilItsDeadline(t *testing.T) {
	r := openTestRegistry(t)
	unlock := lockRegistry(t, r.Path())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	release, err := r.WriteWithin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started := time.Now()
	_, err = r.db.Exec(`CREATE TABLE write_bound_probe (x INTEGER)`)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a bound write waited %s past its 300ms deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want the deadline", err)
	}
	unlock()
	retry, cancelRetry := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRetry()
	release()
	release, err = r.WriteWithin(retry)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	held := lockRegistry(t, r.Path())
	go func() {
		time.Sleep(200 * time.Millisecond)
		held()
	}()
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatalf("a bound transaction did not start once the lock was freed: %v", err)
	}
	tx.Rollback()
}

func TestAWriteBoundToAnEndedDeadlineLandsOnlyWhenTheLockIsFree(t *testing.T) {
	r := openTestRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := r.WriteWithin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := r.db.Exec(`CREATE TABLE write_bound_free (x INTEGER)`); err != nil {
		t.Fatalf("a write past its deadline with the lock free failed: %v", err)
	}
	lockRegistry(t, r.Path())
	started := time.Now()
	_, err = r.db.Exec(`CREATE TABLE write_bound_busy (x INTEGER)`)
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("a write past its deadline waited %s for the lock", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want the ended deadline", err)
	}
}

func TestOneBindingAtATimeAndReleaseRestoresTheBusyTimeout(t *testing.T) {
	r := openTestRegistry(t)
	if got := busyTimeout(t, r); got != int(defaultBusyTimeout.Milliseconds()) {
		t.Fatalf("unbound busy_timeout=%d", got)
	}
	release, err := r.WriteWithin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteWithin(context.Background()); !errors.Is(err, ErrWriteBound) {
		t.Fatalf("second binding err=%v, want ErrWriteBound", err)
	}
	if _, err := r.db.Exec(`CREATE TABLE write_bound_probe (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		defer release()
		panic("command failed")
	}()
	if got := busyTimeout(t, r); got != int(defaultBusyTimeout.Milliseconds()) {
		t.Fatalf("busy_timeout after release=%d", got)
	}
	again, err := r.WriteWithin(context.Background())
	if err != nil {
		t.Fatalf("a binding released by a panic left the registry bound: %v", err)
	}
	again()
	release()
}
