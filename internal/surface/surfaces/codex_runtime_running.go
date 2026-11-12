package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func (c *Codex) RuntimeRunning(sess *surface.Session) bool {
	return sess != nil && sess.ID != "" && c.threadLoaded(sess.ID)
}
