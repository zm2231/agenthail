package registry

import "sync"

// stampQueue holds use stamps waiting to be written. Authenticating a request
// only reads, so no request is held up by a write that waits on the
// registry's busy timeout; the stamps are written after it, by at most one
// writer at a time.
type stampQueue[K comparable, V any] struct {
	write func(K, V) error

	mu      sync.Mutex
	pending map[K]V
	// inflight holds the stamps the writer has taken but not yet written, so
	// a read in between still sees them.
	inflight map[K]V
	writing  bool
	closed   bool
	writes   sync.WaitGroup
}

func newStampQueue[K comparable, V any](write func(K, V) error) *stampQueue[K, V] {
	return &stampQueue[K, V]{write: write}
}

func (q *stampQueue[K, V]) pendingStamp(key K) (V, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if value, ok := q.pending[key]; ok {
		return value, true
	}
	value, ok := q.inflight[key]
	return value, ok
}

func (q *stampQueue[K, V]) stamp(key K, value V) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if q.pending == nil {
		q.pending = map[K]V{}
	}
	q.pending[key] = value
	if q.writing {
		return
	}
	q.writing = true
	q.writes.Add(1)
	go q.drain()
}

// drain writes the pending stamps until none are left. A stamp that fails
// stays pending, unless a newer one replaced it, and the writer stops: the
// next stamp starts it again, and close writes what is left.
func (q *stampQueue[K, V]) drain() {
	defer q.writes.Done()
	for {
		q.mu.Lock()
		pending := q.pending
		q.pending = nil
		if len(pending) == 0 {
			q.writing = false
			q.mu.Unlock()
			return
		}
		q.inflight = pending
		q.mu.Unlock()
		failed := q.writeAll(pending)
		q.mu.Lock()
		q.inflight = nil
		if len(failed) > 0 {
			q.keep(failed)
			q.writing = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
	}
}

func (q *stampQueue[K, V]) writeAll(stamps map[K]V) map[K]V {
	failed := map[K]V{}
	for key, value := range stamps {
		if err := q.write(key, value); err != nil {
			failed[key] = value
		}
	}
	return failed
}

// keep puts failed stamps back as pending where no newer one replaced them.
// The caller holds mu.
func (q *stampQueue[K, V]) keep(failed map[K]V) {
	if q.pending == nil {
		q.pending = map[K]V{}
	}
	for key, value := range failed {
		if _, newer := q.pending[key]; !newer {
			q.pending[key] = value
		}
	}
}

func (q *stampQueue[K, V]) wait() { q.writes.Wait() }

func (q *stampQueue[K, V]) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.writes.Wait()
	q.mu.Lock()
	pending := q.pending
	q.pending = nil
	q.mu.Unlock()
	q.writeAll(pending)
}
