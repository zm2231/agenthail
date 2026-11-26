package surface

import (
	"context"
	"sync"
	"time"
)

// A Scan is one observer pass over many sessions. A surface keeps in it the
// reads every session of the pass would otherwise repeat, such as a listing
// of all of its processes, so the pass makes each of them once.
type Scan struct {
	started time.Time
	mu      sync.Mutex
	shared  map[any]*scanShared
}

type scanShared struct {
	reading sync.Mutex
	value   any
	at      time.Time
}

type scanKey struct{}

func NewScan() *Scan { return &Scan{started: time.Now(), shared: map[any]*scanShared{}} }

// Started is when the scan began. Every read it shares was made after it, so
// what an observation in the scan saw is no older.
func (s *Scan) Started() time.Time { return s.started }

// Attach makes reads under the returned context share the scan's reads.
func (s *Scan) Attach(ctx context.Context) context.Context {
	return context.WithValue(ctx, scanKey{}, s)
}

// ScanShared returns what read returned for key earlier in ctx's scan, while
// that is younger than maxAge, and otherwise reads again. Outside a scan it
// always reads. Concurrent callers wait for one read.
func ScanShared[T any](ctx context.Context, key any, maxAge time.Duration, read func() T) T {
	scan, _ := ctx.Value(scanKey{}).(*Scan)
	if scan == nil {
		return read()
	}
	scan.mu.Lock()
	entry := scan.shared[key]
	if entry == nil {
		entry = &scanShared{}
		scan.shared[key] = entry
	}
	scan.mu.Unlock()
	entry.reading.Lock()
	defer entry.reading.Unlock()
	if value, ok := entry.value.(T); ok && time.Since(entry.at) < maxAge {
		return value
	}
	at := time.Now()
	value := read()
	entry.value, entry.at = value, at
	return value
}
