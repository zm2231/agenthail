package surfaces

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) Ended(ctx context.Context, sess *surface.Session) bool {
	return claudeHosted(sess) && c.NeedsWake(ctx, sess)
}

func (c *Claude) Wake(ctx context.Context, sess *surface.Session) error {
	if !claudeHosted(sess) {
		return fmt.Errorf("%w: only a conversation the Claude host runs is brought back", surface.ErrUnsupported)
	}
	_, err := c.reach(ctx, sess)
	return err
}
