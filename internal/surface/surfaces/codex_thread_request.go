package surfaces

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexThreadStart  = "thread/start"
	codexThreadResume = "thread/resume"
	codexThreadFork   = "thread/fork"
)

func mcpServerConfig(url, token string) map[string]any {
	return map[string]any{"mcp_servers": map[string]any{
		mcpServerName: map[string]any{
			"url": url, "http_headers": map[string]any{"Authorization": "Bearer " + token}, "tool_timeout_sec": mcpToolTimeoutSec,
			"default_tools_approval_mode": "approve",
		},
		CodexRelayServerName: codexRelayDisabled(),
	}}
}

func withMCPConfig(params map[string]any, url, token string) map[string]any {
	return withConfig(params, mcpServerConfig(url, token))
}

// The only sender of thread/start|resume|fork; a new thread's token is minted unbound and claimed once Codex returns its id.
func (c *Codex) codexThreadRequest(ctx context.Context, client codexClient, method string, session *surface.Session, params map[string]any, wait time.Duration) (map[string]any, error) {
	if method == codexThreadResume {
		threadID, _ := params["threadId"].(string)
		defer c.subscriptions.shared(threadID)()
		return c.resumeFollower(ctx, client, session, params, wait)
	}
	credentials := c.mcpCredentials()
	if credentials == nil {
		return client.Request(ctx, method, params, wait)
	}
	switch method {
	case codexThreadStart, codexThreadFork:
		token, ref, err := credentials.Pending(ctx)
		if err != nil {
			return nil, fmt.Errorf("issue the new thread's credential: %w", err)
		}
		response, err := client.Request(ctx, method, withMCPConfig(params, credentials.URL(), token), wait)
		if err != nil {
			return response, err
		}
		result, _ := response["result"].(map[string]any)
		thread, _ := result["thread"].(map[string]any)
		if id := str(thread, "id"); id != "" {
			if err := credentials.Claim(ref, id); err != nil {
				credentials.Unattached(id, fmt.Errorf("claim the new thread's credential: %w", err))
			}
		}
		return response, nil
	}
	return nil, fmt.Errorf("codexThreadRequest does not send %s", method)
}

// The caller holds the thread's subscription gate.
func (c *Codex) resumeFollower(ctx context.Context, client codexClient, session *surface.Session, params map[string]any, wait time.Duration) (map[string]any, error) {
	threadID, _ := params["threadId"].(string)
	response, err := c.joinOrResumeThread(ctx, client, session, params, wait)
	if err == nil {
		c.subscriptions.add(threadID, client)
	}
	return response, err
}

// Codex rebuilds a loaded thread that no client subscribes to when a resume's
// overrides differ from its config, and the rebuild aborts a turn that is just
// starting. So a resume of a thread this app-server already has loaded joins
// it with no overrides; only a cold load carries them.
func (c *Codex) joinOrResumeThread(ctx context.Context, client codexClient, session *surface.Session, params map[string]any, wait time.Duration) (map[string]any, error) {
	threadID, _ := params["threadId"].(string)
	loaded, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil {
		return nil, fmt.Errorf("thread/loaded/list: %w", err)
	}
	if !slices.Contains(loaded, threadID) {
		return c.resumeThread(ctx, client, session, params, wait)
	}
	join := map[string]any{"threadId": threadID}
	if exclude, ok := params["excludeTurns"]; ok {
		join["excludeTurns"] = exclude
	}
	credentials := c.mcpCredentials()
	if credentials == nil {
		return client.Request(ctx, codexThreadResume, join, wait)
	}
	reload, err := c.withOverrides(session, params)
	if err != nil {
		return nil, err
	}
	return c.resumeKeepingMCP(ctx, client, credentials, session, join, reload, wait)
}

func (c *Codex) resumeThread(ctx context.Context, client codexClient, session *surface.Session, params map[string]any, wait time.Duration) (map[string]any, error) {
	params, err := c.withOverrides(session, params)
	if err != nil {
		return nil, err
	}
	if credentials := c.mcpCredentials(); credentials != nil {
		return c.resumeWithCredential(ctx, client, credentials, session, params, wait)
	}
	return client.Request(ctx, codexThreadResume, params, wait)
}

func (c *Codex) withOverrides(session *surface.Session, params map[string]any) (map[string]any, error) {
	store := c.codexConfigStore()
	if store == nil || session == nil || session.ID == "" {
		return params, nil
	}
	overrides, err := store.CodexConfigOverrides(session.ID)
	if err != nil {
		return nil, fmt.Errorf("read the thread's config overrides: %w", err)
	}
	return withConfig(params, overrides), nil
}

// A loaded thread ignores a resume's config, so only a reload replaces a token it holds.
func (c *Codex) resumeWithCredential(ctx context.Context, client codexClient, credentials MCPCredentials, session *surface.Session, params map[string]any, wait time.Duration) (map[string]any, error) {
	if token, ok := credentials.Live(session.ID); ok {
		return client.Request(ctx, codexThreadResume, withMCPConfig(params, credentials.URL(), token), wait)
	}
	return c.resumeKeepingMCP(ctx, client, credentials, session, params, params, wait)
}

// resumeKeepingMCP sends params, then reloads the thread with reload and a
// fresh credential when its agenthail MCP server is not connected.
func (c *Codex) resumeKeepingMCP(ctx context.Context, client codexClient, credentials MCPCredentials, session *surface.Session, params, reload map[string]any, wait time.Duration) (map[string]any, error) {
	response, err := client.Request(ctx, codexThreadResume, params, wait)
	if err != nil {
		return response, err
	}
	if credentials.Current(session.ID) {
		connected, err := codexMCPConnected(ctx, client, session.ID)
		if err != nil {
			credentials.Unattached(session.ID, fmt.Errorf("read the thread's MCP status: %w", err))
			return response, nil
		}
		if connected {
			return response, nil
		}
	}
	if reattached, err := c.reattachMCP(ctx, client, credentials, session.ID, reload, wait); err != nil {
		credentials.Unattached(session.ID, err)
	} else {
		response = reattached
	}
	return response, nil
}

func (c *Codex) mcpCurrent(threadID string) bool {
	credentials := c.mcpCredentials()
	return credentials == nil || credentials.Current(threadID)
}

func (c *Codex) reattachMCP(ctx context.Context, client codexClient, credentials MCPCredentials, threadID string, params map[string]any, wait time.Duration) (map[string]any, error) {
	if busy, err := c.turnInFlight(ctx, client, threadID); err != nil {
		return nil, fmt.Errorf("check for a running turn before reloading: %w", err)
	} else if busy {
		return nil, errors.New("a turn is starting or running, so the thread is not reloaded; the server attaches at a resume once the thread is idle")
	}
	token, err := credentials.Rotate(threadID)
	if err != nil {
		return nil, fmt.Errorf("rotate the credential: %w", err)
	}
	if _, err := client.Request(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID}, 5*time.Second); err != nil {
		return nil, fmt.Errorf("thread/unsubscribe: %w", err)
	}
	response, err := client.Request(ctx, codexThreadResume, withMCPConfig(params, credentials.URL(), token), wait)
	if err != nil {
		return nil, fmt.Errorf("thread/resume with the credential: %w", err)
	}
	connected, err := codexMCPConnected(ctx, client, threadID)
	if err != nil {
		return response, fmt.Errorf("read the thread's MCP status: %w", err)
	}
	if !connected {
		return response, fmt.Errorf("another client keeps the thread loaded without the agenthail MCP server; it attaches at the thread's next load")
	}
	return response, nil
}

func codexMCPConnected(ctx context.Context, client codexClient, threadID string) (bool, error) {
	response, err := client.Request(ctx, "mcpServerStatus/list", map[string]any{"threadId": threadID, "serverName": mcpServerName, "detail": "toolsAndAuthOnly"}, 5*time.Second)
	if err != nil {
		return false, err
	}
	result, _ := response["result"].(map[string]any)
	servers, _ := result["data"].([]any)
	for _, server := range servers {
		entry, _ := server.(map[string]any)
		if str(entry, "name") == mcpServerName && str(entry, "runtimeStatus") == "connected" {
			return true, nil
		}
	}
	return false, nil
}
