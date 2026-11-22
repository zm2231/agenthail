package proxyacct

import "time"

// The proxy fills only when its least-full account does, since it routes around full ones.
func Fill(accounts []Account, now time.Time) (percent float64, resetsAt time.Time, ok bool) {
	unknown := false
	for _, account := range accounts {
		if account.Provider != "claude" || account.Status == StatusDisabled || account.Status == StatusUnavailable {
			continue
		}
		used, frees := 100.0, account.NextRetry
		if live(account, now) {
			used, frees = accountFill(drainReadings(account, now))
		}
		if !ok || used < percent {
			percent = used
		}
		if !frees.After(now) {
			unknown = true
		} else if resetsAt.IsZero() || frees.Before(resetsAt) {
			resetsAt = frees
		}
		ok = true
	}
	if unknown {
		resetsAt = time.Time{}
	}
	return percent, resetsAt, ok
}

func accountFill(readings map[string]Drain) (float64, time.Time) {
	used, resets := 0.0, time.Time{}
	for _, kind := range []string{WindowSession, WindowWeek} {
		reading, found := readings[kind]
		if !found || !reading.HasPercent {
			continue
		}
		percent := reading.Percent
		if reading.Ceiling > 0 {
			percent = min(100, percent*100/reading.Ceiling)
		}
		if percent >= used {
			used, resets = percent, reading.ResetsAt
		}
	}
	return used, resets
}
