package daemon

import (
	"context"
	"sync"
)

// backgroundWork owns the goroutines that outlive the call that starts them:
// announcements, withdrawals, queued commands and refreshes. Each one runs
// with the daemon's lifetime context, and stop cancels that context and
// returns once every one has finished, so none writes to the registry after
// the daemon has stopped. Work started after stop does not run.
type backgroundWork struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	idle    *sync.Cond
	running int
	stopped bool
}

func newBackgroundWork() *backgroundWork {
	ctx, cancel := context.WithCancel(context.Background())
	work := &backgroundWork{ctx: ctx, cancel: cancel}
	work.idle = sync.NewCond(&work.mu)
	return work
}

// start reports false, without running work, once the daemon has stopped.
func (b *backgroundWork) start(work func(ctx context.Context)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	b.running++
	go func() {
		defer b.finish()
		work(b.ctx)
	}()
	return true
}

func (b *backgroundWork) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.running--
	if b.running == 0 {
		b.idle.Broadcast()
	}
}

func (b *backgroundWork) wait() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.running > 0 {
		b.idle.Wait()
	}
}

func (b *backgroundWork) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.cancel()
	b.wait()
}
