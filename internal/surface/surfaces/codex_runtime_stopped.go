package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) RuntimeStopped(ctx context.Context, sess *surface.Session) bool {
	if sess.RuntimeKind != surface.RuntimeCodexAppServer || !c.managed {
		return false
	}
	client, err := c.home.dialManaged(ctx)
	if err != nil {
		return true
	}
	client.Close()
	return false
}
