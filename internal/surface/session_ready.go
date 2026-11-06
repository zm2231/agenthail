package surface

import "context"

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
