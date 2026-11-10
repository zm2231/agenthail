package surfaces

import (
	"context"
	"encoding/json"
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

// managedCodexClient is one connection to Codex's app-server. A single reader
// goroutine owns every socket read: it hands each response to the request
// waiting on its id and buffers everything else as a notification.
type managedCodexClient struct {
	ws      *websocket.Conn
	writeMu sync.Mutex

	mu            sync.Mutex
	next          int
	waiters       map[int]chan codexReply
	received      int64
	notifications []codexEvent
	err           error
	closed        chan struct{}
	notified      chan struct{}
	server        *codexServer
}

const managedNotificationLimit = 1000

// codexReply is a response with the receive count of the notifications the
// connection read before it.
type codexReply struct {
	message map[string]any
	after   int64
}

func newManagedCodexClient(ws *websocket.Conn) *managedCodexClient {
	client := &managedCodexClient{
		ws:       ws,
		next:     1,
		waiters:  map[int]chan codexReply{},
		closed:   make(chan struct{}),
		notified: make(chan struct{}, 1),
	}
	go client.read()
	return client
}

type codexServer struct {
	path string
	info os.FileInfo
}

// restarted reports whether the managed app-server that was listening on s no
// longer listens there. A restarted server binds a new socket file, and every
// turn the old one ran is gone.
func (s *codexServer) restarted() bool {
	current, err := os.Stat(s.path)
	return err != nil || !os.SameFile(current, s.info) || !current.ModTime().Equal(s.info.ModTime())
}

func (h CodexHome) dialManaged(ctx context.Context) (*managedCodexClient, error) {
	socketPath := h.socketPath()
	info, err := os.Stat(socketPath)
	if err != nil {
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
	client := newManagedCodexClient(ws)
	client.server = &codexServer{path: socketPath, info: info}
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
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(map[string]any{"method": method, "params": params})
}

func (c *managedCodexClient) read() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			c.fail(fmt.Errorf("managed Codex app-server connection closed: %w", err))
			return
		}
		var message map[string]any
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			c.fail(fmt.Errorf("managed Codex app-server sent invalid JSON: %w", err))
			return
		}
		_ = json.Unmarshal(data, &envelope)
		method, _ := message["method"].(string)
		if method == "" {
			if id, ok := message["id"].(float64); ok {
				c.mu.Lock()
				waiter := c.waiters[int(id)]
				delete(c.waiters, int(id))
				after := c.received
				c.mu.Unlock()
				if waiter != nil {
					waiter <- codexReply{message: message, after: after}
				}
			}
			continue
		}
		params, _ := message["params"].(map[string]any)
		c.mu.Lock()
		// A server request carries an id the answer must repeat exactly,
		// whether Codex sent a number or a string.
		c.received++
		c.notifications = append(c.notifications, codexEvent{Method: method, Params: params, RequestID: envelope.ID, received: c.received})
		if len(c.notifications) > managedNotificationLimit {
			c.notifications = c.notifications[len(c.notifications)-managedNotificationLimit:]
		}
		c.mu.Unlock()
		select {
		case c.notified <- struct{}{}:
		default:
		}
	}
}

// fail ends the connection: every pending and later request returns err.
func (c *managedCodexClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	c.waiters = map[int]chan codexReply{}
	close(c.closed)
}

func (c *managedCodexClient) Request(ctx context.Context, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	response, _, err := c.requestOrdered(ctx, method, params, wait)
	return response, err
}

func (c *managedCodexClient) requestOrdered(ctx context.Context, method string, params map[string]any, wait time.Duration) (map[string]any, int64, error) {
	waiter := make(chan codexReply, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, 0, err
	}
	id := c.next
	c.next++
	c.waiters[id] = waiter
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}
	c.writeMu.Lock()
	err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		forget()
		return nil, 0, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case reply := <-waiter:
		if envelope, ok := reply.message["error"].(map[string]any); ok {
			code, _ := envelope["code"].(float64)
			return nil, reply.after, &codexRPCError{Method: method, Code: int(code), Message: str(envelope, "message")}
		}
		return reply.message, reply.after, nil
	case <-ctx.Done():
		forget()
		return nil, 0, ctx.Err()
	case <-timer.C:
		forget()
		return nil, 0, fmt.Errorf("%s: app-server request timed out after %s: %w", method, wait, os.ErrDeadlineExceeded)
	case <-c.closed:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return nil, 0, err
	}
}

// codexRPCError is a JSON-RPC error the app-server answered a request with.
type codexRPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("%s RPC error (%d): %s", e.Method, e.Code, e.Message)
}

// Respond answers a server request on this connection.
func (c *managedCodexClient) Respond(id json.RawMessage, result any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(map[string]any{"id": id, "result": result})
}

func (c *managedCodexClient) DrainNotifications() []codexEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := c.notifications
	c.notifications = nil
	return events
}

func (c *managedCodexClient) failed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err != nil
}

// Notified fires after a notification arrives; DrainNotifications returns it.
func (c *managedCodexClient) Notified() <-chan struct{} { return c.notified }

func (c *managedCodexClient) Done() <-chan struct{} { return c.closed }

// Close fails the client at once, so the thread subscriptions it joined drop
// it before its read loop sees the socket close.
func (c *managedCodexClient) Close() error {
	c.fail(net.ErrClosed)
	return c.ws.Close()
}

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
	return c.loadForDirectInput(ctx, client, sess, "")
}

// loadForDirectInput loads the thread when Codex does not have it loaded,
// with model as the resume's override; Codex ignores the override for a
// thread it already has loaded.
func (c *Codex) loadForDirectInput(ctx context.Context, client codexClient, sess *surface.Session, model string) error {
	readResponse, err := client.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
	if err == nil && codexDirectInputAccepted(readResponse) && c.mcpCurrent(sess.ID) {
		return nil
	}
	params := codexResumeParams(sess)
	if model != "" {
		params["model"] = model
	}
	if _, err := c.codexThreadRequest(ctx, client, codexThreadResume, sess, params, 10*time.Second); err != nil {
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
	sess.Source, sess.Status, sess.Ended, sess.WaitingOn = codexSource(thread["source"]), codexStatus(thread["status"]), codexThreadEnded(thread["status"]), codexSessionWaiting(sess.ID, thread["status"])
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

func (h CodexHome) acquireWriteLock(ctx context.Context) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(h.writeLockPath()), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(h.writeLockPath(), os.O_CREATE|os.O_RDWR, 0600)
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

// errCodexThreadMoving refuses a write to a thread a move holds; the write
// is retried and, once the move is done, goes to the host the thread moved to.
var errCodexThreadMoving = errors.New("the Codex thread is moving to another host")

// lockedCodexThread takes the thread's lock file, creating it, and returns it
// once the file it locked is still the one at the path; a release unlinks the
// path before unlocking, so a lock on an unlinked file holds nothing.
func lockedCodexThread(ctx context.Context, path string) (*os.File, error) {
	for {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			if sameFile(file, path) {
				return file, nil
			}
			releaseCodexWriteLock(file)
			continue
		}
		_ = file.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func sameFile(file *os.File, path string) bool {
	held, err := file.Stat()
	if err != nil {
		return false
	}
	named, err := os.Stat(path)
	return err == nil && os.SameFile(held, named)
}

// holdCodexThread waits for any write to the thread to end and holds it
// against every later one until release. The lock file exists only while a
// move holds the thread: after taking it, the hold passes through the write
// lock once, so a write that started before the file existed has ended and
// every later write finds the file.
func (h CodexHome) holdThread(ctx context.Context, threadID string) (release func(), err error) {
	if !validCodexMoveThread(threadID) {
		return nil, fmt.Errorf("Codex thread id %q is not valid", threadID)
	}
	path := h.threadLockPath(threadID)
	file, err := lockedCodexThread(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("wait for writes to Codex thread %s: %w", threadID, err)
	}
	release = func() {
		_ = os.Remove(path)
		releaseCodexWriteLock(file)
	}
	fence, err := h.acquireWriteLock(ctx)
	if err != nil {
		release()
		return nil, fmt.Errorf("wait for writes to Codex thread %s: %w", threadID, err)
	}
	releaseCodexWriteLock(fence)
	return release, nil
}

// acquireCodexThreadWrite takes the Codex write lock for a write to one
// thread, refusing while a move holds the thread. A lock file no move holds
// was left by a move that did not finish, and is removed.
func (h CodexHome) acquireThreadWrite(ctx context.Context, threadID string) (release func(), err error) {
	lock, err := h.acquireWriteLock(ctx)
	if err != nil {
		return nil, err
	}
	if !validCodexMoveThread(threadID) {
		return func() { releaseCodexWriteLock(lock) }, nil
	}
	path := h.threadLockPath(threadID)
	thread, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return func() { releaseCodexWriteLock(lock) }, nil
	}
	if err != nil {
		releaseCodexWriteLock(lock)
		return nil, err
	}
	if err := syscall.Flock(int(thread.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = thread.Close()
		releaseCodexWriteLock(lock)
		return nil, errCodexThreadMoving
	}
	if sameFile(thread, path) {
		_ = os.Remove(path)
	}
	releaseCodexWriteLock(thread)
	return func() { releaseCodexWriteLock(lock) }, nil
}

func (c *Codex) requestSession(ctx context.Context, sess *surface.Session, writable bool, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	return c.requestSessionWith(ctx, sess, writable, func(client codexClient) (map[string]any, error) {
		return client.Request(ctx, method, params, wait)
	})
}

func (c *Codex) requestSessionWith(ctx context.Context, sess *surface.Session, writable bool, run func(codexClient) (map[string]any, error)) (map[string]any, error) {
	if writable {
		release, err := c.home.acquireThreadWrite(ctx, sess.ID)
		if err != nil {
			return nil, surface.DeliveryUnavailable(err)
		}
		defer release()
	}
	if !writable {
		client, err := c.openReader(ctx, sess)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return run(client)
	}
	client, err := c.openWriter(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	response, err := run(client)
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
