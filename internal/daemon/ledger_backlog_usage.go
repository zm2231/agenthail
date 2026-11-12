package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/ledger"
)

func (d *Daemon) ledgerUsageReader(ctx context.Context, now time.Time) ledger.UsageReader {
	local := sync.OnceValue(func() []usageSet { return d.localUsageSets(ctx, now) })
	remote := sync.OnceValue(func() []usageSet { return d.remoteUsageSets(ctx) })
	here := d.localHost().ID
	return func(target ledger.ParkUsageTarget) ledger.Headroom {
		sets := remote
		if target.HostID == "" || target.HostID == here {
			sets = local
		}
		return parkHeadroom(sets(), target, now)
	}
}

func parkHeadroom(sets []usageSet, target ledger.ParkUsageTarget, now time.Time) ledger.Headroom {
	for _, set := range sets {
		if set.Provider != target.Provider || set.Problem != "" || (target.HostID != "" && set.HostID != target.HostID) {
			continue
		}
		if target.Provider == "claude" && !(target.Root == "" && set.Primary || target.Root != "" && (set.Name == target.Root || set.Root == target.Root)) {
			continue
		}
		read, room, frees := claudepick.Headroom(limitMoveCandidates([]usageSet{set}, now)[0])
		return ledger.Headroom{Known: read, Room: room, Frees: frees}
	}
	return ledger.Headroom{}
}
