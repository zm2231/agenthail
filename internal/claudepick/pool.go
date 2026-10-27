package claudepick

import (
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Member struct {
	Account string
	Session *Window
	Weekly  *Window
	RetryAt time.Time
}

func pooled(c Candidate, policy Policy, now time.Time) Candidate {
	if c.Login != surface.ClaudeLoginProxy || len(c.Pool) == 0 {
		return c
	}
	members := make([]Candidate, len(c.Pool))
	for i, member := range c.Pool {
		members[i] = Candidate{Account: member.Account, Session: member.Session, Weekly: member.Weekly}
	}
	var open []int
	for i, member := range c.Pool {
		if !member.RetryAt.After(now) && under(members[i], policy) {
			open = append(open, i)
		}
	}
	if len(open) > 0 {
		sort.SliceStable(open, func(a, b int) bool { return load(members[open[a]], policy) < load(members[open[b]], policy) })
		return withMember(c, members[open[0]])
	}
	first := -1
	var firstAt time.Time
	for i, member := range c.Pool {
		at := member.RetryAt
		if !under(members[i], policy) {
			if windows := frees(members[i], policy); windows.After(at) {
				at = windows
			}
		}
		if first < 0 || at.Before(firstAt) {
			first, firstAt = i, at
		}
	}
	c = withMember(c, members[first])
	c.FullUntil = firstAt
	return c
}

func Pooled(pool []Member, policy Policy, now time.Time) Candidate {
	return pooled(Candidate{Login: surface.ClaudeLoginProxy, Pool: pool}, policy, now)
}

func withMember(c Candidate, member Candidate) Candidate {
	c.Best, c.Account, c.Session, c.Weekly = member.Account, member.Account, member.Session, member.Weekly
	return c
}
