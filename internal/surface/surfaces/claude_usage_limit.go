package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) UsageLimitTranscripts() func(*surface.Session) string {
	var transcripts func(string) []string
	if roots, err := surface.ClaudeConfigRoots(c.home); err == nil {
		transcripts = c.transcripts.pass(roots)
	}
	return func(sess *surface.Session) string {
		if sess.Transcript != "" || transcripts == nil {
			return sess.Transcript
		}
		return onlyTranscript(transcripts(sess.ID))
	}
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
