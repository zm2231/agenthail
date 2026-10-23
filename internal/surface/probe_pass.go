package surface

import (
	"context"
	"sync"
)

type probePassKey struct{}

type probePass struct {
	mu      sync.Mutex
	results map[any]*probeResult
}

type probeResult struct {
	done   chan struct{}
	shared bool
	value  any
	err    error
}

// WithProbePass returns a context whose calls, such as one catalog pass's
// listing, health check and runtime status, share each runtime probe. Keep it
// to the calls of one pass: anything that outlives the pass keeps its first
// answer.
func WithProbePass(ctx context.Context) context.Context {
	return context.WithValue(ctx, probePassKey{}, &probePass{results: map[any]*probeResult{}})
}

// ProbeOnce runs probe once per pass and key and gives every later call in
// the pass the same answer. Outside a pass it always runs probe. A probe
// whose caller's context ended is not shared; the next call probes again.
func ProbeOnce[T any](ctx context.Context, key any, probe func() (T, error)) (T, error) {
	pass, _ := ctx.Value(probePassKey{}).(*probePass)
	if pass == nil {
		return probe()
	}
	for {
		pass.mu.Lock()
		result, found := pass.results[key]
		if !found {
			result = &probeResult{done: make(chan struct{})}
			pass.results[key] = result
		}
		pass.mu.Unlock()
		if !found {
			value, err := probe()
			pass.mu.Lock()
			if err != nil && ctx.Err() != nil {
				delete(pass.results, key)
			} else {
				result.shared, result.value, result.err = true, value, err
			}
			pass.mu.Unlock()
			close(result.done)
			return value, err
		}
		select {
		case <-result.done:
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
		if result.shared {
			return result.value.(T), result.err
		}
	}
}
