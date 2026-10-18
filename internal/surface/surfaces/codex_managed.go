package surfaces

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
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
	ws            *websocket.Conn
	mu            sync.Mutex
	next          int
	notifications []codexEvent
}

func managedCodexSocketPath() string {
	return filepath.Join(codexHome(), "app-server-control", "app-server-control.sock")
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
			if method, _ := response["method"].(string); method != "" {
				params, _ := response["params"].(map[string]any)
				c.notifications = append(c.notifications, codexEvent{Method: method, Params: params})
				if len(c.notifications) > 1000 {
					c.notifications = c.notifications[len(c.notifications)-1000:]
				}
			}
			continue
		}
		if envelope, ok := response["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%s RPC error (%v): %s", method, envelope["code"], str(envelope, "message"))
		}
		return response, nil
	}
}

func (c *managedCodexClient) DrainNotifications() []codexEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := c.notifications
	c.notifications = nil
	return events
}

func (c *managedCodexClient) Close() error { return c.ws.Close() }

func (c *Codex) openDesktop(ctx context.Context) (codexClient, error) {
	conn, err := c.dial(ctx)
	if err != nil {
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
	if err != nil && strings.Contains(err.Error(), "renderer bridge was not found") {
		c.bridgeErr = err
		c.bridgeRetry = now.Add(10 * time.Second)
		return err
	}
	c.bridgeErr = nil
	c.bridgeRetry = time.Time{}
	return err
}

func (c *Codex) EnsureWritable(ctx context.Context, sess *surface.Session) error {
	if sess == nil {
		return fmt.Errorf("Codex session is required")
	}
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	if err := runtimes.ownershipUnknown(); err != nil {
		return err
	}
	c.resolveRoute(ctx, runtimes, sess)
	if surface.IsReadOnlySession(sess) {
		return fmt.Errorf("%s", surface.ReadOnlySessionReason(sess))
	}
	return nil
}

// codexWriterConflict reports Codex's refusal to load a thread whose writer
// lock another process holds. Codex returns the generic invalid-request code,
// so the message is the only signal.
func codexWriterConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already has an active writer")
}

// classifyCodexWrite turns Codex's writer-conflict response to any request
// into a terminal ownership conflict and leaves every other error as is.
func classifyCodexWrite(err error) error {
	if !codexWriterConflict(err) {
		return err
	}
	return surface.DeliveryTerminal(fmt.Errorf("Codex session is owned by another runtime; release that writer before delivery: %w", err), surface.DeliveryOwnershipConflict)
}

// codexOperationOutcome classifies a failed write whose request may have
// reached Codex: failures decided before or by Codex keep their class, and
// anything else is an unknown outcome.
func codexOperationOutcome(err error) error {
	if surface.IsDeliveryUnavailable(err) || surface.IsDeliveryTerminal(err) {
		return err
	}
	return surface.DeliveryOutcomeUnknown(err)
}

// requireDirectInput makes the route's runtime the thread's live writer:
// a loaded thread that accepts direct input is used as is, otherwise the
// thread is resumed there and must then accept direct input.
func (c *Codex) requireDirectInput(ctx context.Context, client codexClient, sess *surface.Session) error {
	readResponse, err := client.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
	if err == nil && codexDirectInputAccepted(readResponse) {
		return nil
	}
	if _, err := client.Request(ctx, "thread/resume", codexResumeParams(sess), 10*time.Second); err != nil {
		return classifyCodexWrite(fmt.Errorf("thread/resume: %w", err))
	}
	readResponse, err = client.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return fmt.Errorf("thread/read after resume: %w", err)
	}
	if !codexDirectInputAccepted(readResponse) {
		return errors.New("Codex loaded the session without direct-input capability")
	}
	result, _ := readResponse["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	sess.Source, sess.Status = codexSource(thread["source"]), codexStatus(thread["status"])
	return nil
}

func codexResumeParams(sess *surface.Session) map[string]any {
	params := map[string]any{
		"threadId":     sess.ID,
		"history":      nil,
		"excludeTurns": true,
	}
	if sess.Cwd != "" {
		params["cwd"] = sess.Cwd
	}
	return params
}

func codexWriteLockPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "codex-write.lock")
}

func acquireCodexWriteLock(ctx context.Context) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(codexWriteLockPath()), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(codexWriteLockPath(), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return file, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func releaseCodexWriteLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (c *Codex) requestSession(ctx context.Context, sess *surface.Session, writable bool, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	var lock *os.File
	var err error
	if writable {
		lock, err = acquireCodexWriteLock(ctx)
		if err != nil {
			return nil, surface.DeliveryUnavailable(err)
		}
		defer releaseCodexWriteLock(lock)
	}
	if !writable {
		client, err := c.openReader(ctx, sess)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.Request(ctx, method, params, wait)
	}
	client, err := c.openWriter(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := client.Request(ctx, method, params, wait)
	return response, classifyCodexWrite(err)
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
