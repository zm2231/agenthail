package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) headerMap(_, _ string) map[string]string {
	return map[string]string{
		"user-agent":                chromeUA,
		"accept":                    "application/json",
		"accept-language":           "en-US,en;q=0.9",
		"origin":                    "https://claude.ai",
		"referer":                   "https://claude.ai/code/",
		"anthropic-version":         "2023-06-01",
		"anthropic-beta":            "ccr-byoc-2025-07-29",
		"anthropic-client-platform": "web_claude_ai",
		"anthropic-client-feature":  "ccr",
		"anthropic-client-version":  "1.0.0",
	}
}

func toCse(bridgeID string) string {
	s := bridgeID
	s = strings.TrimPrefix(s, "https://claude.ai/code/")
	s = strings.TrimPrefix(s, "session_")
	s = strings.TrimPrefix(s, "cse_")
	return "cse_" + s
}

var claudeSendRequest = sidecarRequestWithCookies

func (c *Claude) postMessage(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return c.postContent(ctx, sess, message)
}

func (c *Claude) postContent(ctx context.Context, sess *surface.Session, content any) (*surface.SendResult, error) {
	if c.localOnly {
		return nil, surface.DeliveryUnavailable(errors.New("Claude Remote Control is not available on this install; reach the session through its local Claude Code process"))
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if sess.Bridge == "" {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude conversation %s has no Remote Control attachment", sess.ID))
	}
	cookies, err := c.cookieSource(ctx, sess)
	if err != nil {
		return nil, cookieSourceError(err)
	}
	uuid := surface.MessageUUID(ctx)
	if uuid == "" {
		uuid = newUUID()
	}
	cse := toCse(sess.Bridge)
	body := map[string]any{
		"events": []map[string]any{{
			"payload": map[string]any{
				"type": "user", "uuid": uuid, "session_id": sess.Bridge,
				"parent_tool_use_id": nil,
				"message":            map[string]any{"role": "user", "content": content},
			},
		}},
	}
	bodyBytes, _ := json.Marshal(body)
	headers := c.headerMap("", sess.Bridge)
	headers["content-type"] = "application/json"
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	status, respBody, err := request(ctx, "POST",
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), cookies, 30*time.Second)
	if surface.IsDeliveryUnavailable(err) {
		return nil, err
	}
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	if strings.Contains(respBody, "Just a moment") {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("cloudflare challenge (cf_clearance may need refresh)"))
	}
	switch status {
	case http.StatusBadRequest:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryInvalidRequest)
	case http.StatusUnauthorized:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAuthenticationNeeded)
	case http.StatusForbidden:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAccessDenied)
	case http.StatusNotFound:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryTargetMissing)
	}
	if status != http.StatusOK {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)))
	}
	return &surface.SendResult{UUID: uuid, Accepted: true}, nil
}
