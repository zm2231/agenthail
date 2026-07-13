package surfaces

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexTransportDesktop  = "desktop"
	codexTransportManaged  = "managed"
	codexTransportReadOnly = "readOnly"
)

type codexClient interface {
	Request(context.Context, string, map[string]any, time.Duration) (map[string]any, error)
	Close() error
}

type desktopCodexClient struct {
	owner *Codex
	conn  *cdpConn
}

func (c *desktopCodexClient) Request(ctx context.Context, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	return c.owner.rpc(ctx, c.conn, method, params, wait)
}

func (c *desktopCodexClient) Close() error { return c.conn.close() }

type managedCodexClient struct {
	ws   *websocket.Conn
	mu   sync.Mutex
	next int
}

func managedCodexSocketPath() string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

func dialManagedCodex(ctx context.Context) (*managedCodexClient, error) {
	socketPath := managedCodexSocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		return nil, fmt.Errorf("managed Codex app-server is unavailable: %w", err)
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	ws, response, err := dialer.DialContext(ctx, "ws://localhost/rpc", http.Header{})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("connect managed Codex app-server: HTTP %d: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("connect managed Codex app-server: %w", err)
	}
	client := &managedCodexClient{ws: ws, next: 1}
	if _, err := client.Request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agenthail", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, 5*time.Second); err != nil {
		_ = client.Close()
		return nil, err
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (c *managedCodexClient) notify(method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteJSON(map[string]any{"method": method, "params": params})
}

func (c *managedCodexClient) Request(ctx context.Context, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.next
	c.next++
	if err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := c.ws.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	for {
		var response map[string]any
		if err := c.ws.ReadJSON(&response); err != nil {
			return nil, err
		}
		responseID, ok := response["id"].(float64)
		if !ok || int(responseID) != id {
			continue
		}
		if envelope, ok := response["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%s RPC error (%v): %s", method, envelope["code"], str(envelope, "message"))
		}
		return response, nil
	}
}

func (c *managedCodexClient) Close() error { return c.ws.Close() }

func (c *Codex) openDesktop(ctx context.Context) (codexClient, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.ensureDesktopHook(ctx, conn); err != nil {
		_ = conn.close()
		return nil, err
	}
	return &desktopCodexClient{owner: c, conn: conn}, nil
}

func (c *Codex) ensureDesktopHook(ctx context.Context, conn *cdpConn) error {
	c.bridgeMu.Lock()
	defer c.bridgeMu.Unlock()
	now := time.Now()
	if c.bridgeTarget == conn.target && c.bridgeErr != nil && now.Before(c.bridgeRetry) {
		return c.bridgeErr
	}
	err := c.ensureHooked(ctx, conn)
	c.bridgeTarget = conn.target
	if err != nil && strings.Contains(err.Error(), "request dispatcher") {
		c.bridgeErr = err
		c.bridgeRetry = now.Add(10 * time.Second)
		return err
	}
	c.bridgeErr = nil
	c.bridgeRetry = time.Time{}
	return err
}

func (c *Codex) openSession(ctx context.Context, sess *surface.Session, writable bool) (codexClient, error) {
	if writable && (sess.Transport == "" || sess.Transport == codexTransportReadOnly) {
		return nil, fmt.Errorf("Codex terminal session is read only; start a writable session with 'agenthail codex'")
	}
	if sess.Transport == codexTransportManaged {
		return c.openManaged(ctx)
	}
	return c.openDesktop(ctx)
}

func (c *Codex) requestSession(ctx context.Context, sess *surface.Session, writable bool, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	client, err := c.openSession(ctx, sess, writable)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Request(ctx, method, params, wait)
}

func codexSource(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if object, ok := value.(map[string]any); ok {
		for key := range object {
			return key
		}
	}
	return ""
}

func codexTransport(source string, status any, managed, desktopReachable bool) string {
	if source == "vscode" {
		if desktopReachable {
			return codexTransportDesktop
		}
		return codexTransportReadOnly
	}
	if managed && codexStatus(status) != surface.SessionStatus("notLoaded") {
		return codexTransportManaged
	}
	return codexTransportReadOnly
}
