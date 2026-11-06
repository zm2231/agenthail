package surfaces

import (
	"context"
	"os"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// streamRoute is the session a stream lists commands for. Claude rewrites its
// session record when Remote Control starts or stops, so the route is read
// again whenever that record changes, without a transcript write.
type streamRoute struct {
	c       *Claude
	record  string
	mu      sync.Mutex
	session surface.Session
	stamp   string
}

func (c *Claude) newStreamRoute(ctx context.Context, sess *surface.Session) *streamRoute {
	pid := sess.PID
	if sess.HolderPID > 0 {
		pid = sess.HolderPID
	}
	r := &streamRoute{c: c, session: *sess}
	if pid > 0 {
		r.record = c.peerRecordPath(pid)
	}
	r.reread(ctx)
	return r
}

func (r *streamRoute) current() surface.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

// reread reports whether Remote Control came or went since the last read.
func (r *streamRoute) reread(ctx context.Context) bool {
	if r.record == "" {
		return false
	}
	stamp := ""
	if info, err := os.Stat(r.record); err == nil {
		stamp = surface.Stamp(info).String()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if stamp == r.stamp {
		return false
	}
	r.stamp = stamp
	bridge := r.session.Bridge
	r.c.RefreshRoute(ctx, &r.session)
	return r.session.Bridge != bridge
}
