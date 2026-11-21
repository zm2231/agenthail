package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path, err := c.sessionTranscript(session)
	if err != nil {
		return nil, err
	}
	page, err := readTranscriptPage(ctx, path, claudeTranscript{settled: func(at time.Time) bool { return c.settledAfter(session, at) }}, request.Before, request.Limit)
	if err != nil {
		return nil, err
	}
	return surface.BoundSessionRead(session, page), nil
}

func (c *Claude) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	return readSessionReply(ctx, c, sess, limit)
}
