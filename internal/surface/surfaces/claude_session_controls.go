package surfaces

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) Compact(ctx context.Context, sess *surface.Session) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "compact"})
	return err
}

func (c *Claude) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if err := c.EnsureWritable(ctx, sess); err != nil {
		return "", err
	}
	if name != "" {
		result, err := c.Control(ctx, sess, surface.ControlRequest{Action: "model", Value: name})
		if err != nil {
			return "", fmt.Errorf("model switch rejected: %w", err)
		}
		return result.Output, nil
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	return readClaudeLatestModel(ctx, path)
}

func (c *Claude) Interrupt(ctx context.Context, sess *surface.Session) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "interrupt"})
	return err
}

func (c *Claude) Steer(ctx context.Context, sess *surface.Session, message string) error {
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: "steer", Value: message})
	return err
}

func (c *Claude) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	return surface.Tail(ctx, c, sess, n)
}
