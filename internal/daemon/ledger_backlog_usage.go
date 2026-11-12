package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
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

func parkUsageSet(sets []usageSet, target ledger.ParkUsageTarget) (usageSet, bool) {
	for _, set := range sets {
		if set.Provider != target.Provider || set.Problem != "" || (target.HostID != "" && set.HostID != target.HostID) {
			continue
		}
		if target.Provider == "claude" && !(target.Root == "" && set.Primary || target.Root != "" && (set.Name == target.Root || set.Root == target.Root)) {
			continue
		}
		return set, true
	}
	return usageSet{}, false
}

func parkHeadroom(sets []usageSet, target ledger.ParkUsageTarget, now time.Time) ledger.Headroom {
	set, ok := parkUsageSet(sets, target)
	if !ok || slices.ContainsFunc(set.Meters, func(m usageMeter) bool { return m.Aged }) {
		return ledger.Headroom{}
	}
	read, room, frees := claudepick.Headroom(limitMoveCandidates([]usageSet{set}, now)[0])
	return ledger.Headroom{Known: read, Room: room, Frees: frees}
}

func (d *Daemon) stampUsagePark(ctx context.Context, path string, body []byte, now time.Time) []byte {
	if path != "/api/v1/ledger/work" {
		return body
	}
	limited := func(session string) (registry.LimitResume, bool) {
		stop, ok, err := d.Registry.LimitResumeFor(session)
		return stop, ok && err == nil
	}
	return stampUsageParkFrom(body, d.localHost().ID, func() []usageSet { return d.localUsageSets(ctx, now) }, limited, now)
}

func stampUsageParkFrom(body []byte, here string, local func() []usageSet, limited func(session string) (registry.LimitResume, bool), now time.Time) []byte {
	var req ledger.WorkRequest
	if json.Unmarshal(body, &req) != nil || req.Action != ledger.WorkPark || req.Park == nil || req.Park.Kind != ledger.ParkUsage || req.Park.Usage == nil {
		return body
	}
	target := *req.Park.Usage
	if target.HostID != "" && target.HostID != here {
		return body
	}
	target.HostID = here
	sets := local()
	if target.Provider == "claude" && target.Root == "" {
		for _, session := range []string{req.ForSession, req.SessionID} {
			if stop, found := limited(session); session != "" && found {
				if _, known := parkUsageSet(sets, ledger.ParkUsageTarget{Provider: "claude", HostID: here, Root: stop.AccountKey}); known {
					target.Root = stop.AccountKey
					if target.ResetsAt.IsZero() && stop.ResetsAt.After(now) {
						target.ResetsAt = stop.ResetsAt
					}
					break
				}
			}
		}
	}
	set, ok := parkUsageSet(sets, target)
	if ok && target.Provider == "claude" && target.Root == "" {
		target.Root = set.Name
	}
	if ok && target.ResetsAt.IsZero() {
		if room := parkHeadroom([]usageSet{set}, target, now); room.Known && !room.Room {
			target.ResetsAt = room.Frees
		}
	}
	var fields, park map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["park"], &park) != nil {
		return body
	}
	usage, err := json.Marshal(target)
	if err != nil {
		return body
	}
	park["usage"] = usage
	if fields["park"], err = json.Marshal(park); err != nil {
		return body
	}
	stamped, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return stamped
}
