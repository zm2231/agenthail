package surfaces

import (
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
)

// errSigninUnconfirmed is a sign-in check that could not ask every profile
// which claude.ai account it is signed into.
var errSigninUnconfirmed = errors.New("could not confirm which sign-in profile is signed into claude.ai")

// remoteControlUnusable reports a session this host cannot reach through
// Remote Control: an install without it, a session with no bridge, or one
// whose Remote Control this host could not sign into a moment ago.
func (c *Claude) remoteControlUnusable(sess *surface.Session) bool {
	return c.localOnly || nativeClaudeOnly(sess) || c.remoteControlRefused(sess)
}

// remoteControlRefused reports a session whose Remote Control request failed
// to sign in within the last cookieHeaderTTL. The session is reached on its
// socket meanwhile; the mark expires, so a profile signed in later is used.
func (c *Claude) remoteControlRefused(sess *surface.Session) bool {
	if sess == nil || sess.ID == "" {
		return false
	}
	c.refusedMu.Lock()
	defer c.refusedMu.Unlock()
	until, ok := c.refused[sess.ID]
	if ok && !time.Now().Before(until) {
		delete(c.refused, sess.ID)
		return false
	}
	return ok
}

// remoteControlUnauthenticated reports a Remote Control request that could
// not sign in, so nothing reached the session, as a changed route when the
// session's native socket can take the message instead. Without a socket the
// failure stands.
func (c *Claude) remoteControlUnauthenticated(sess *surface.Session, err error) error {
	if sess == nil || sess.ID == "" || sess.Transport != "uds" || claudeHosted(sess) || !remoteSigninFailure(err) {
		return err
	}
	c.refusedMu.Lock()
	if c.refused == nil {
		c.refused = map[string]time.Time{}
	}
	c.refused[sess.ID] = time.Now().Add(browsercookie.HeaderTTL)
	c.refusedMu.Unlock()
	return fmt.Errorf("%w: %w; Claude conversation %s takes it on its messaging socket", surface.ErrRouteChanged, err, sess.ID)
}

func remoteSigninFailure(err error) bool {
	if errors.Is(err, errSigninUnconfirmed) {
		return true
	}
	switch surface.DeliveryTerminalReason(err) {
	case surface.DeliverySigninOff, surface.DeliverySigninUnreadable, surface.DeliverySigninUnavailable, surface.DeliveryAuthenticationNeeded, surface.DeliveryAccessDenied:
		return true
	}
	return false
}
