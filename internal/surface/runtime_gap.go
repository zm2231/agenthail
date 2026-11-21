package surface

import (
	"context"
	"sync"
)

type runtimeGapKey struct{}

// RuntimeGap is what one listing ran without, set by the adapter that listed.
type RuntimeGap struct {
	mu      sync.Mutex
	without string
}

func WithRuntimeGap(ctx context.Context) (context.Context, *RuntimeGap) {
	gap := &RuntimeGap{}
	return context.WithValue(ctx, runtimeGapKey{}, gap), gap
}

func NoteRuntimeGap(ctx context.Context, without string) {
	if gap, ok := ctx.Value(runtimeGapKey{}).(*RuntimeGap); ok {
		gap.mu.Lock()
		gap.without = without
		gap.mu.Unlock()
	}
}

func (g *RuntimeGap) Without() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.without
}
