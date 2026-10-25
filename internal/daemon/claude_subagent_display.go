package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeSubagentDisplay struct {
	surface.ClaudeSubagentLink
	Display SessionDisplay `json:"display"`
}

func displayClaudeSubagents(page claudeSubagentPage) ([]claudeSubagentDisplay, int) {
	links := make([]claudeSubagentDisplay, 0, len(page.links))
	working := 0
	for _, link := range page.links {
		status := surface.StatusIdle
		if link.Working {
			status = surface.StatusBusy
			working++
		}
		links = append(links, claudeSubagentDisplay{link, sessionDisplay(dashboardSession{Status: status}, time.Now())})
	}
	return links, working
}
