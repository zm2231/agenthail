package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) UsageLimitTranscript(sess *surface.Session) string {
	if sess.Transcript != "" {
		return sess.Transcript
	}
	return c.transcriptPath(sess)
}

func (c *Claude) ReadUsageLimit(ctx context.Context, sess *surface.Session, transcript string) (surface.UsageLimitEvidence, error) {
	read := *sess
	read.Transcript = transcript
	observation, err := c.observeTurn(ctx, &read)
	if err != nil {
		return surface.UsageLimitEvidence{}, err
	}
	return surface.UsageLimitEvidence{Account: observation.UsageAccount, SettledAt: observation.SettledAt, Stop: observation.LimitStop}, nil
}
