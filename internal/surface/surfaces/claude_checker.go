package surfaces

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) StartChecker(ctx context.Context, start surface.CheckerStart) (*surface.Session, error) {
	if start.Run == "" || start.Message == "" {
		return nil, fmt.Errorf("a checker needs its run and first message")
	}
	opened, err := c.host.client.Open(ctx, claudehost.OpenRequest{SessionID: start.Run, Cwd: start.Cwd, Checker: &claudehost.CheckerOpen{Run: start.Run}})
	if err != nil {
		return nil, claudeHostDeliveryError(fmt.Errorf("open the narrowing checker: %w", err))
	}
	if opened.Checker != start.Run {
		_ = c.host.client.Close(ctx, start.Run)
		return nil, fmt.Errorf("the Claude host opened %s without the checker sandbox", start.Run)
	}
	session := &surface.Session{ID: start.Run, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: opened.PID,
		Name: "narrowing checker", Cwd: start.Cwd, Status: surface.StatusIdle, Source: "agenthail", ConfiguredModel: opened.Model, LastActive: time.Now()}
	if _, err := c.sendHostTurn(ctx, session, start.Message, "", nil); err != nil {
		_ = c.host.client.Close(ctx, start.Run)
		return nil, err
	}
	return session, nil
}
