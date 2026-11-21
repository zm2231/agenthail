package surface

import "time"

type HolderChecker interface {
	StillHolds(conversation string, holder Holder) bool
	StartedBefore(holder Holder, at time.Time) bool
}
