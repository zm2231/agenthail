package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func (c *Claude) RuntimeRunning(sess *surface.Session) bool {
	if sess == nil || sess.Surface != surface.KindClaude || sess.ID == "" {
		return false
	}
	if _, live := c.liveRecord(*sess); live {
		return true
	}
	if sess.HolderPID <= 0 {
		return false
	}
	held := *sess
	held.PID = sess.HolderPID
	_, live := c.liveRecord(held)
	return live
}
