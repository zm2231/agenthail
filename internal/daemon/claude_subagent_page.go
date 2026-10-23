package daemon

import (
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeSubagentsShown is how many finished subagents session metadata lists
// beside the working ones unless the client asks for all of them.
const claudeSubagentsShown = 8

type claudeSubagentPage struct {
	links []surface.ClaudeSubagentLink
	total int
}

func newClaudeSubagentPage(links []surface.ClaudeSubagentLink, all bool) claudeSubagentPage {
	ordered := append([]surface.ClaudeSubagentLink{}, links...)
	slices.SortStableFunc(ordered, func(a, b surface.ClaudeSubagentLink) int {
		if a.Working != b.Working {
			if a.Working {
				return -1
			}
			return 1
		}
		return b.LastActive.Compare(a.LastActive)
	})
	page := claudeSubagentPage{links: ordered, total: len(ordered)}
	if all {
		return page
	}
	working := 0
	for working < len(ordered) && ordered[working].Working {
		working++
	}
	page.links = ordered[:min(len(ordered), working+claudeSubagentsShown)]
	return page
}
