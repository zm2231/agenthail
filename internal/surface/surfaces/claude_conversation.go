package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

// ConversationExists counts a process that holds the conversation, because a
// conversation that just started has not written its transcript yet.
func (c *Claude) ConversationExists(ctx context.Context, sess *surface.Session) (bool, error) {
	if c.resolveTranscript(sess.ID) != "" {
		return true, nil
	}
	state, err := c.conversation(ctx, sess.ID, claudePin{})
	if err != nil && !state.alive {
		return false, err
	}
	return state.alive, nil
}
