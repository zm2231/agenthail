package cli

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdClaudeRuns(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail runs [--json]")
	}
	adapter := a.adapters().ForSurface(surface.KindClaude)
	observer, ok := adapter.(surface.ClaudeRunObserver)
	if !ok {
		return fmt.Errorf("Claude run observation is unavailable")
	}
	runs, err := observer.ObserveClaudeRuns(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude job records: %w", err)
	}
	links, err := observer.ObserveAllClaudeSubagents(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude subagent records: %w", err)
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"runs": runs, "subagents": links})
	}
	for _, run := range runs {
		fmt.Printf("%s\t%s\t%s\t%s\n", run.JobID, run.ProviderState, run.SessionID, run.RunType)
	}
	for _, link := range links {
		fmt.Printf("subagent\t%s\t%s\t%s\n", link.ParentSessionID, link.AgentID, link.TranscriptPath)
	}
	return nil
}
