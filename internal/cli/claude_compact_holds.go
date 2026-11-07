package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	idleCompactionHoldsShown   = 5
	idleCompactionHoldsTimeout = 5 * time.Second
)

func (a *App) idleCompactionHolds() ([]daemon.IdleCompactionHold, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), idleCompactionHoldsTimeout)
	defer cancel()
	var body struct {
		Holds []daemon.IdleCompactionHold `json:"idleCompactionHolds"`
	}
	if err := client.getJSON(ctx, "/api/v1/claude/compact-settings", nil, &body); err != nil {
		return nil, err
	}
	if body.Holds == nil {
		body.Holds = []daemon.IdleCompactionHold{}
	}
	return body.Holds, nil
}

func describeIdleCompactionHolds(root string, holds []daemon.IdleCompactionHold) []string {
	var lines []string
	for i := len(holds) - 1; i >= 0 && len(lines) < idleCompactionHoldsShown; i-- {
		hold := holds[i]
		if !surface.SameClaudeRoot(hold.Root, root) {
			continue
		}
		name := hold.Session
		if name == "" {
			name = hold.SessionID
		}
		lines = append(lines, fmt.Sprintf("  not compacted: %s at %d tokens, %s: %s", name, hold.ContextTokens, relTime(hold.At), hold.Reason))
	}
	return lines
}
