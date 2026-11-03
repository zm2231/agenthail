package surfaces

import (
	"context"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/claudehost"
)

// The only caller of the host's open: every conversation it opens, except a sandboxed checker, carries its agenthail credential.
func (c *Claude) hostOpen(ctx context.Context, request claudehost.OpenRequest) (claudehost.Session, error) {
	credentials := c.mcpCredentials()
	if credentials == nil || request.Checker != nil {
		return c.host.client.Open(ctx, request)
	}
	id := request.Resume
	if id == "" {
		id = request.SessionID
	}
	token, err := claudeHostToken(ctx, credentials, id, request.Resume != "")
	if err != nil {
		credentials.Unattached(id, err)
		return c.host.client.Open(ctx, request)
	}
	request.MCP = &claudehost.MCPServer{URL: credentials.URL(), Token: token}
	opened, err := c.host.client.Open(ctx, request)
	if err == nil && opened.MCPError != "" {
		credentials.Unattached(id, errors.New(opened.MCPError))
	}
	return opened, err
}

func (c *Claude) ReplaceMCPCredential(ctx context.Context, sessionID, url, token string) error {
	_, err := c.host.client.ReplaceMCP(ctx, sessionID, claudehost.MCPServer{URL: url, Token: token})
	return err
}

func claudeHostToken(ctx context.Context, credentials MCPCredentials, id string, resume bool) (string, error) {
	if id == "" {
		return "", errors.New("the conversation has no id before it opens")
	}
	if resume {
		if token, ok := credentials.Live(id); ok {
			return token, nil
		}
		if token, err := credentials.Rotate(id); err == nil {
			return token, nil
		}
	}
	token, ref, err := credentials.Pending(ctx)
	if err != nil {
		return "", fmt.Errorf("issue the conversation's credential: %w", err)
	}
	if err := credentials.Claim(ref, id); err != nil {
		return "", fmt.Errorf("claim the conversation's credential: %w", err)
	}
	return token, nil
}
