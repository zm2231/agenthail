package surface

import "context"

// Keeper brings an ended conversation back without starting a turn.
type Keeper interface {
	Ended(ctx context.Context, sess *Session) bool
	Wake(ctx context.Context, sess *Session) error
}
