package surfaces

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
	release, err := acquireCodexThreadWrite(ctx, sess.ID)
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
	release, err := acquireCodexThreadWrite(ctx, sess.ID)
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
	if _, err := conn.Request(ctx, "turn/steer", map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          codexUserInput(message, images),
	}, 5*time.Second); err != nil {
		return surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/steer: %w", err))
	}
	return nil
}
