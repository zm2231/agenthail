package surfaces

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Ended(ctx context.Context, sess *surface.Session) bool {
	if sess.RuntimeKind != surface.RuntimeCodexAppServer {
		return false
	}
	probe := *sess
	client, err := c.openRoute(ctx, &probe)
	if err != nil {
		return true
	}
	defer client.Close()
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
	return err != nil || !codexDirectInputAccepted(response)
}

func (c *Codex) Wake(ctx context.Context, sess *surface.Session) error {
	if sess.RuntimeKind != surface.RuntimeCodexAppServer {
		return fmt.Errorf("%w: only a thread the Codex app-server runs is brought back", surface.ErrUnsupported)
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	client, err := c.openWriter(ctx, sess)
	if err != nil {
		return err
	}
	return client.Close()
}
