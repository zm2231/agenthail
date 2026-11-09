package surfaces

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Ended(ctx context.Context, sess *surface.Session) bool {
	if sess.RuntimeKind != surface.RuntimeCodexAppServer {
		return false
	}
	client, err := c.openKeptRoute(ctx, sess)
	if errors.Is(err, surface.ErrDeliveryHeld) {
		return false
	}
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
	release, err := acquireCodexThreadWrite(ctx, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer release()
	client, err := c.openKeptRoute(ctx, sess)
	if err != nil {
		return err
	}
	defer client.Close()
	return c.requireDirectInput(ctx, client, sess)
}

// openKeptRoute opens the app-server for a thread it owns. A thread Desktop
// has loaded or would take is held by Desktop and left alone.
func (c *Codex) openKeptRoute(ctx context.Context, sess *surface.Session) (codexClient, error) {
	runtimes := c.openRuntimes(ctx)
	if err := runtimes.ownershipUnknown(); err != nil {
		runtimes.Close()
		return nil, surface.DeliveryUnavailable(err)
	}
	held := surface.DeliveryUnavailable(fmt.Errorf("%w: Codex Desktop holds thread %s", surface.ErrDeliveryHeld, sess.ID))
	if runtimes.desktopLoaded[sess.ID] {
		runtimes.Close()
		return nil, held
	}
	probe := *sess
	c.resolveRoute(ctx, runtimes, &probe)
	if probe.Transport != codexTransportManaged {
		runtimes.Close()
		return nil, held
	}
	if runtimes.managed != nil {
		runtimes.release(runtimes.managed)
		return runtimes.managed, nil
	}
	runtimes.Close()
	return c.openManaged(ctx)
}
