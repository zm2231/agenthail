package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
)

const (
	defaultBusyTimeout = 15 * time.Second
	boundBusyTimeout   = 50 * time.Millisecond
	boundRetryFirst    = 10 * time.Millisecond
	boundRetryMax      = 100 * time.Millisecond
	sqliteBusy         = 5
)

var ErrWriteBound = errors.New("registry writes are already bound to a deadline")

// The driver ignores a context while SQLite is busy, so a bound write waits briefly per attempt and retries until the context ends.
type writeDB struct {
	raw *sql.DB

	mu    sync.Mutex
	bound context.Context
}

func (w *writeDB) boundContext() context.Context {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bound
}

// WriteWithin bounds this registry's writes by ctx until release; one binding at a time.
func (r *Registry) WriteWithin(ctx context.Context) (release func(), err error) {
	w := r.db
	w.mu.Lock()
	if w.bound != nil {
		w.mu.Unlock()
		return nil, ErrWriteBound
	}
	w.bound = ctx
	w.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			w.bound = nil
			w.mu.Unlock()
			_, _ = w.raw.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", defaultBusyTimeout.Milliseconds()))
		})
	}, nil
}

func (w *writeDB) Begin() (*sql.Tx, error) {
	return retryBusy(w, func(ctx context.Context) (*sql.Tx, error) { return w.raw.BeginTx(ctx, nil) })
}

func (w *writeDB) Exec(query string, args ...any) (sql.Result, error) {
	return retryBusy(w, func(ctx context.Context) (sql.Result, error) { return w.raw.ExecContext(ctx, query, args...) })
}

func (w *writeDB) QueryRow(query string, args ...any) *sql.Row {
	return w.raw.QueryRow(query, args...)
}

func (w *writeDB) Query(query string, args ...any) (*sql.Rows, error) {
	return w.raw.Query(query, args...)
}

func (w *writeDB) Close() error {
	return w.raw.Close()
}

func retryBusy[T any](w *writeDB, attempt func(context.Context) (T, error)) (T, error) {
	ctx := w.boundContext()
	if ctx == nil {
		return attempt(context.Background())
	}
	wait := boundRetryFirst
	for {
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, fmt.Errorf("registry write: %w", err)
		}
		if _, err := w.raw.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", boundBusyTimeout.Milliseconds())); err != nil {
			var zero T
			return zero, fmt.Errorf("registry write: %w", err)
		}
		value, err := attempt(ctx)
		if !isBusy(err) {
			return value, err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, fmt.Errorf("registry write: %w (%v)", ctx.Err(), err)
		case <-timer.C:
		}
		wait = min(wait*2, boundRetryMax)
	}
}

func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqliteBusy
}
