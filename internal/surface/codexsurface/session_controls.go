package codexsurface

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) GoalClear(ctx context.Context, sess *surface.Session) error {
	_, err := c.requestSession(ctx, sess, true, "thread/goal/clear", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Compact(ctx context.Context, sess *surface.Session) error {
	_, err := c.requestSessionWith(ctx, sess, true, func(client codexClient) (map[string]any, error) {
		return c.startTurn(ctx, client, sess.ID, "thread/compact/start", map[string]any{"threadId": sess.ID}, 10*time.Second)
	})
	return err
}

func (c *Codex) Interrupt(ctx context.Context, sess *surface.Session) error {
	return c.interruptTurn(ctx, sess, "")
}

func (c *Codex) InterruptTurn(ctx context.Context, sess *surface.Session, expectedTurnID string) error {
	return c.interruptTurn(ctx, sess, expectedTurnID)
}

func (c *Codex) interruptTurn(ctx context.Context, sess *surface.Session, expectedTurnID string) error {
	release, err := c.home.acquireThreadWrite(ctx, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer release()
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return err
	}
	defer conn.Close()
	return c.interruptActiveTurn(ctx, conn, sess.ID, expectedTurnID)
}

func (c *Codex) interruptActiveTurn(ctx context.Context, conn codexClient, threadID, expectedTurnID string) error {
	turnID, err := c.activeTurnID(ctx, conn, threadID, expectedTurnID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to interrupt")
	}
	if expectedTurnID != "" && turnID != expectedTurnID {
		return fmt.Errorf("selected turn changed before interruption; confirm the active turn again")
	}
	_, err = conn.Request(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Steer(ctx context.Context, sess *surface.Session, message string) error {
	return c.SteerWithImages(ctx, sess, message, nil)
}

func (c *Codex) SteerWithImages(ctx context.Context, sess *surface.Session, message string, images surface.Uploads) error {
	if err := refuseCodexComposerCommand(message); err != nil {
		return err
	}
	message = surface.LabelSender(ctx, message)
	release, err := c.home.acquireThreadWrite(ctx, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer release()
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return err
	}
	defer conn.Close()
	turnID, err := c.activeTurnID(ctx, conn, sess.ID, "")
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if turnID == "" {
		return surface.DeliveryUnavailable(fmt.Errorf("%w (use 'send' instead)", surface.ErrNothingToSteer))
	}
	params := map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          codexUserInput(message, images),
	}
	setCodexClientMessageID(ctx, params)
	delivered := c.withTurnContext(ctx, conn, *sess, params)
	if _, err := conn.Request(ctx, "turn/steer", params, 5*time.Second); err != nil {
		return codexOperationOutcome(fmt.Errorf("turn/steer: %w", err))
	}
	delivered()
	return nil
}

// StopRun ends a thread's run in Codex's app-server daemon, which is shared,
// by interrupting its turn rather than stopping the process. Dropping
// Agenthail's subscriptions lets Codex unload the idle thread; a viewer that
// follows it again keeps it loaded but runs nothing. A thread the Codex app has
// loaded is the app's.
func (c *Codex) StopRun(ctx context.Context, sess *surface.Session) (surface.RunStop, error) {
	release, err := c.home.acquireThreadWrite(ctx, sess.ID)
	if err != nil {
		return surface.RunStop{}, surface.DeliveryUnavailable(err)
	}
	defer release()
	runtimes := c.openRuntimes(ctx)
	if err := runtimes.ownershipUnknown(); err != nil {
		runtimes.Close()
		return surface.RunStop{}, surface.DeliveryUnavailable(err)
	}
	switch {
	case runtimes.desktopLoaded[sess.ID]:
		runtimes.Close()
		return surface.RunStop{NotStopped: "it runs in the Codex app"}, nil
	case !runtimes.managedLoaded[sess.ID]:
		runtimes.Close()
		return surface.RunStop{}, nil
	}
	client := runtimes.managed
	runtimes.release(client)
	defer client.Close()
	turnID, err := c.activeTurnID(ctx, client, sess.ID, "")
	if err != nil {
		return surface.RunStop{}, surface.DeliveryUnavailable(err)
	}
	if turnID != "" {
		if _, err := client.Request(ctx, "turn/interrupt", map[string]any{"threadId": sess.ID, "turnId": turnID}, 5*time.Second); err != nil {
			return surface.RunStop{}, fmt.Errorf("turn/interrupt: %w", err)
		}
	}
	unlock := c.subscriptions.exclusive(sess.ID)
	defer unlock()
	for _, follower := range c.subscriptions.clients(sess.ID) {
		c.subscriptions.remove(sess.ID, follower)
		if _, err := follower.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
			_ = follower.Close()
		}
	}
	if _, err := client.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
		return surface.RunStop{}, fmt.Errorf("thread/unsubscribe: %w", err)
	}
	return surface.RunStop{Stopped: true}, nil
}
