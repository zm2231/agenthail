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

func Pooled(pool []Member, now time.Time) Candidate {
	c := Candidate{Login: surface.ClaudeLoginProxy, Pool: pool}
	if len(pool) == 0 {
		return c
	}
	members := make([]Candidate, len(pool))
	for i, member := range pool {
		members[i] = Candidate{Account: member.Account, Session: member.Session, Weekly: member.Weekly}
	}
	var open []int
	for i, member := range pool {
		if !member.RetryAt.After(now) && under(members[i]) {
			open = append(open, i)
		}
	}
	if len(open) > 0 {
		sort.SliceStable(open, func(a, b int) bool { return load(members[open[a]]) < load(members[open[b]]) })
		return withMember(c, members[open[0]])
	}
	first := -1
	var firstAt time.Time
	for i, member := range pool {
		at := member.RetryAt
		if !under(members[i]) {
			if windows := frees(members[i]); windows.After(at) {
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

func withMember(c Candidate, member Candidate) Candidate {
	c.Best, c.Account, c.Session, c.Weekly = member.Account, member.Account, member.Session, member.Weekly
	return c
}
