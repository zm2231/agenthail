package surfaces

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
)

func (c *Claude) Health(ctx context.Context) error {
	processes, err := c.conversations(ctx)
	native, bridged := false, false
	for _, process := range processes {
		native = native || process.session.Transport == "uds"
		bridged = bridged || process.session.Bridge != ""
	}
	if c.localOnly {
		if native {
			return nil
		}
		return err
	}
	cookies, signinErr := c.cookieSource(ctx, nil)
	if signin.IsOff(signinErr) || signin.IsUnavailable(signinErr) {
		if bridged {
			return signinErr
		}
		return err
	}
	if native {
		return nil
	}
	if signinErr != nil {
		return signinErr
	}
	if _, err := sidecarPath(); err != nil {
		return err
	}
	if c.cookieBridge == "" || !fileExists(c.cookieBridge) {
		return fmt.Errorf("Claude cookie bridge not found (set AGENTHAIL_COOKIE_BRIDGE or install cookie.mjs alongside agenthail)")
	}
	status, body, err := sidecarRequestWithCookies(ctx, "GET", "https://claude.ai/api/organizations", c.headerMap("", ""), "", cookies, 15*time.Second)
	if err != nil {
		return fmt.Errorf("Claude authentication probe: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("Claude authentication probe returned HTTP %d: %s", status, diagnosticExcerpt(body))
	}
	return nil
}
