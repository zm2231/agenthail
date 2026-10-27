package claudepick

import (
	"sort"

	"github.com/zm2231/agenthail/internal/surface"
)

func MoveTarget(candidates []Candidate, from string, policy Policy) (Pick, bool) {
	var open []Candidate
	for _, candidate := range candidates {
		if candidate.Root == from || candidate.Login != surface.ClaudeLoginClaudeAI || !known(candidate) || !under(candidate, policy) {
			continue
		}
		open = append(open, candidate)
	}
	if len(open) == 0 {
		return Pick{}, false
	}
	sort.SliceStable(open, func(i, j int) bool { return load(open[i], policy) < load(open[j], policy) })
	target := open[0]
	return Pick{Root: target.Root, Name: target.Name, Account: target.Account, Reason: "most headroom, " + readings(target)}, true
}
