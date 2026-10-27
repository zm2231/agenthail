package cliproxyapi

import (
	"context"
	"net/http"
)

func (c *Client) SetPreference(ctx context.Context, account string, preference float64) error {
	_, err := c.manage(ctx, http.MethodPatch, "/v0/management/auth-files/fields", map[string]any{"name": account, "quota_preference": preference})
	return err
}

func (c *Client) ReleaseSessions(ctx context.Context, account string) error {
	_, err := c.manage(ctx, http.MethodPost, "/v0/management/auth-files/release-sessions", map[string]string{"name": account})
	return err
}
