package surface

import (
	"context"
	"time"
)

// IdleCompactionState is what a conversation's record says about compacting
// it while idle. LastRequestAt is its newest main-thread model request, zero
// when none was found. OneHourCache is whether its newest request that wrote
// the prompt cache wrote it for an hour. CompactedSince is a compaction after
// that request.
type IdleCompactionState struct {
	LastRequestAt  time.Time
	OneHourCache   bool
	ContextTokens  int64
	CompactedSince bool
}

type IdleCompactionReader interface {
	IdleCompactionState(ctx context.Context, sess *Session) (IdleCompactionState, error)
}
