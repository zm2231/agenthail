package surface

import (
	"context"
	"time"
)

// SessionStartTimeout bounds a new session's open and first turn. A starter
// whose provider bounds its own open waits this long rather than for its
// caller's deadline, so the provider decides whether a slow start opened.
const SessionStartTimeout = 90 * time.Second

type sessionReadyKey struct{}

// A starter calls ready after the session has its id and before its first turn runs.
func WithSessionReady(ctx context.Context, ready func(Session) error) context.Context {
	return context.WithValue(ctx, sessionReadyKey{}, ready)
}

func SessionReady(ctx context.Context, session Session) error {
	ready, _ := ctx.Value(sessionReadyKey{}).(func(Session) error)
	if ready == nil {
		return nil
	}
	return ready(session)
}
