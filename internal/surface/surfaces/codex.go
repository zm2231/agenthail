package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

// Codex drives Codex Desktop sessions via CDP into the main V8 inspector (path B).
// Codex must be launched with --inspect=127.0.0.1:9230.
// We Runtime.evaluate inside main to reach the app-server ChildProcess via
// process._getActiveHandles(), write JSON-RPC to its stdin, and read responses
// off stdout using a coexisting line-splitter hook (ids >= 900000).
type Codex struct {
	mainURL string
}

func NewCodex(inspectorURL string) *Codex {
	if inspectorURL == "" {
		inspectorURL = "ws://127.0.0.1:9230"
	}
	return &Codex{mainURL: inspectorURL}
}

func (c *Codex) Name() surface.SurfaceKind { return surface.KindCodex }

func (c *Codex) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true,
		Compact: true, Model: false, Interrupt: true, Steer: true, Fork: true,
	}
}

// conn wraps a CDP websocket with a request/response id scheme.
type cdpConn struct {
	ws   *websocket.Conn
	mu   sync.Mutex
	next int
}

func (c *Codex) dial(ctx context.Context) (*cdpConn, error) {
	wsURL, err := c.resolveInspectorURL(ctx)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, _, err := d.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("connect main inspector at %s: %w (is Codex running with --inspect=9230?)", wsURL, err)
	}
	return &cdpConn{ws: ws, next: 1}, nil
}

// resolveInspectorURL fetches the node inspector target list and returns the
// webSocketDebuggerUrl for the node (main) context. Node inspector requires the
// /<uuid> path suffix; a bare ws://host:port fails the handshake.
func (c *Codex) resolveInspectorURL(ctx context.Context) (string, error) {
	httpURL := strings.Replace(c.mainURL, "ws://", "http://", 1)
	httpURL = strings.Replace(httpURL, "wss://", "https://", 1)
	req, _ := http.NewRequestWithContext(ctx, "GET", httpURL+"/json", nil)
	resp, err := localHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect main inspector at %s: %w (is Codex running with --inspect=9230?)", httpURL, err)
	}
	defer resp.Body.Close()
	var targets []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", fmt.Errorf("parse inspector targets: %w", err)
	}
	for _, t := range targets {
		if t["type"] == "node" {
			if u, ok := t["webSocketDebuggerUrl"].(string); ok && u != "" {
				return u, nil
			}
		}
	}
	return "", fmt.Errorf("no node inspector target found at %s", httpURL)
}

func (c *cdpConn) close() error { return c.ws.Close() }

// evaluate runs a JS expression in the main process and returns its value.
func (c *cdpConn) evaluate(ctx context.Context, expr string, timeout time.Duration) (any, error) {
	c.mu.Lock()
	id := c.next
	c.next++
	c.mu.Unlock()

	req := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":   expr,
			"awaitPromise": true,
			"returnByValue": true,
		},
	}
	c.mu.Lock()
	err := c.ws.WriteJSON(req)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.ws.SetReadDeadline(time.Now().Add(timeout))
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if int(msg["id"].(float64)) != id {
			continue
		}
		result, _ := msg["result"].(map[string]any)
		res, _ := result["result"].(map[string]any)
		if sub, _ := res["subtype"].(string); sub == "error" {
			desc, _ := res["description"].(string)
			return nil, fmt.Errorf("eval error: %s", desc)
		}
		return res["value"], nil
	}
	return nil, fmt.Errorf("timeout waiting for eval response")
}

// hookJS installs the stdout line-splitter hook ONCE. Idempotent via __agenthailHooked flag.
const hookJS = `
(() => {
  if (globalThis.__agenthailHooked) return 'already';
  const h = (process._getActiveHandles ? process._getActiveHandles() : []);
  let child = null;
  for (const x of h) {
    if (x && x.pid && ((x.spawnargs||[]).join(' ').includes('app-server'))) { child = x; break; }
  }
  if (!child) return 'no-child';
  globalThis.__agenthailBuf = '';
  globalThis.__agenthailResponses = [];
  child.stdout.on('data', (d) => {
    globalThis.__agenthailBuf += d.toString();
    let i;
    while ((i = globalThis.__agenthailBuf.indexOf('\n')) >= 0) {
      const line = globalThis.__agenthailBuf.slice(0, i);
      globalThis.__agenthailBuf = globalThis.__agenthailBuf.slice(i + 1);
      try {
        const o = JSON.parse(line);
        if (o.id && o.id >= 900000) globalThis.__agenthailResponses.push(o);
        if (o.method) globalThis.__agenthailResponses.push(o);
      } catch {}
    }
  });
  globalThis.__agenthailChild = child;
  globalThis.__agenthailHooked = true;
  return 'hooked';
})()
`

// rpcJS returns a JS snippet that writes one JSON-RPC request to the app-server stdin.
func rpcJS(id int, method string, params map[string]any) string {
	paramsJSON, _ := json.Marshal(params)
	return fmt.Sprintf(`(()=>{try{const c=globalThis.__agenthailChild;if(!c)return'no-child';c.stdin.write(JSON.stringify({jsonrpc:'2.0',id:%d,method:%s,params:%s})+'\n');return 'sent'}catch(e){return 'ERR '+e.message}})()`,
		id, strconv.Quote(method), string(paramsJSON))
}

// collectJS drains captured responses with the given id.
func collectJS(id int) string {
	return fmt.Sprintf(`(()=>{const r=(globalThis.__agenthailResponses||[]).filter(x=>x.id===%d);return JSON.stringify(r.map(x=>({id:x.id,ok:!!x.result,err:x.error&&(x.error.message||'').slice(0,200),result:x.result})))})()`, id)
}

func clearResponseJS(id int) string {
	return fmt.Sprintf(`(()=>{globalThis.__agenthailResponses=(globalThis.__agenthailResponses||[]).filter(x=>x.id!==%d);return 'ok'})()`, id)
}

func (c *Codex) ensureHooked(ctx context.Context, conn *cdpConn) error {
	v, err := conn.evaluate(ctx, hookJS, 5*time.Second)
	if err != nil {
		return err
	}
	s, _ := v.(string)
	if s == "no-child" {
		return fmt.Errorf("no app-server child process found in Codex main (is a session active?)")
	}
	return nil
}

// rpc sends one JSON-RPC method and waits for the matching response.
func (c *Codex) rpc(ctx context.Context, conn *cdpConn, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	id := 900000 + int(time.Now().UnixNano()%99999)
	conn.evaluate(ctx, clearResponseJS(id), 2*time.Second)

	if _, err := conn.evaluate(ctx, rpcJS(id, method, params), 2*time.Second); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		v, err := conn.evaluate(ctx, collectJS(id), 2*time.Second)
		if err != nil {
			return nil, err
		}
		s, _ := v.(string)
		if s == "" || s == "[]" {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var arr []map[string]any
		if json.Unmarshal([]byte(s), &arr) != nil || len(arr) == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return arr[0], nil
	}
	return nil, fmt.Errorf("timeout waiting for %s response", method)
}

func (c *Codex) List(ctx context.Context) ([]surface.Session, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := c.rpc(ctx, conn, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]any{"name": "agenthail", "version": "1.0"},
		"capabilities":    map[string]any{},
	}, 3*time.Second); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	resp, err := c.rpc(ctx, conn, "thread/list", map[string]any{}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/list: %w", err)
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	var out []surface.Session
	for _, t := range threads {
		m, _ := t.(map[string]any)
		sess := surface.Session{
			ID:      str(m, "id"),
			Surface: surface.KindCodex,
			Name:    surface.DeriveName(str(m, "name"), str(m, "preview"), 60),
			Cwd:     str(m, "cwd"),
			Status:  codexStatus(str(m, "status")),
		}
		if ts, ok := m["recencyAt"].(float64); ok && ts > 0 {
			sess.LastActive = time.Unix(int64(ts), 0) // Codex timestamps are in seconds
		}
		out = append(out, sess)
	}
	return out, nil
}

func codexStatus(s string) surface.SessionStatus {
	switch strings.ToLower(s) {
	case "idle":
		return surface.StatusIdle
	case "busy", "running", "in_progress", "inprogress":
		return surface.StatusBusy
	case "":
		return surface.StatusUnknown
	default:
		return surface.SessionStatus(s)
	}
}

func (c *Codex) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, target) ||
			strings.Contains(strings.ToLower(s.Cwd), lower) ||
			strings.Contains(strings.ToLower(s.Name), lower) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no codex session matched '%s'", target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s cwd=%s", m.ID, m.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	return &matches[0], nil
}

// activeTurnID returns the id of the currently-running turn for a thread, or
// "" if idle. Mirrors the reference codex-control activeTurnId logic.
func (c *Codex) activeTurnID(ctx context.Context, conn *cdpConn, threadID string) (string, error) {
	resp, err := c.rpc(ctx, conn, "thread/turns/list", map[string]any{"threadId": threadID}, 5*time.Second)
	if err != nil {
		return "", nil
	}
	result, _ := resp["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	for _, t := range turns {
		m, _ := t.(map[string]any)
		status, _ := m["status"].(map[string]any)
		stype, _ := status["type"].(string)
		if stype == "running" || stype == "in_progress" || stype == "inProgress" {
			if id, ok := m["id"].(string); ok {
				return id, nil
			}
		}
	}
	return "", nil
}

func (c *Codex) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := c.rpc(ctx, conn, "thread/resume", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
		return nil, fmt.Errorf("thread/resume: %w", err)
	}
	// If a turn is already running, the message must queue and be delivered by
	// the daemon on turn/completed (native Codex queueing, surfaced via the
	// registry). Sending turn/start now would clobber the active turn.
	if active, _ := c.activeTurnID(ctx, conn, sess.ID); active != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	resp, err := c.rpc(ctx, conn, "turn/start", map[string]any{
		"threadId": sess.ID,
		"input":    []map[string]any{{"type": "text", "text": message}},
	}, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("turn/start: %w", err)
	}
	_ = resp
	return &surface.SendResult{UUID: sess.ID, Accepted: true}, nil
}

func (c *Codex) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	resp, err := c.rpc(ctx, conn, "thread/read", map[string]any{
		"threadId":      sess.ID,
		"includeTurns":  true,
	}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := resp["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	turns, _ := thread["turns"].([]any)
	last := ""
	for _, t := range turns {
		m, _ := t.(map[string]any)
		items, _ := m["items"].([]any)
		for _, it := range items {
			im, _ := it.(map[string]any)
			if im["type"] == "agentMessage" {
				if txt, ok := im["text"].(string); ok && txt != "" {
					last = txt
				}
			}
		}
	}
	return &surface.ReplyResult{Text: last, Done: last != ""}, nil
}

func (c *Codex) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	lastCount := 0
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		v, err := conn.evaluate(ctx, `(()=>{const r=(globalThis.__agenthailResponses||[]).filter(x=>!x.id||x.id<900000);return JSON.stringify(r.map(x=>({method:x.method,p:x.params})))})()`, 2*time.Second)
		if err != nil {
			return err
		}
		s, _ := v.(string)
		var notifs []map[string]any
		if s != "" {
			json.Unmarshal([]byte(s), &notifs)
		}
		for len(notifs) > lastCount {
			n := notifs[lastCount]
			lastCount++
			method, _ := n["method"].(string)
			p, _ := n["p"].(map[string]any)
			switch {
			case strings.Contains(method, "agentMessage"):
				if txt, _ := p["text"].(string); txt != "" {
					onEvent(surface.StreamEvent{Kind: "text", Text: txt})
				}
			case strings.Contains(method, "tool"):
				if name, _ := p["name"].(string); name != "" {
					onEvent(surface.StreamEvent{Kind: "tool_use", Text: name})
				}
			case strings.Contains(method, "turn/completed"), strings.Contains(method, "turn/completion"):
				onEvent(surface.StreamEvent{Kind: "done"})
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func (c *Codex) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/goal/set", map[string]any{
		"threadId": sess.ID,
		"text":     text,
	}, 5*time.Second)
	return err
}

func (c *Codex) GoalClear(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/goal/clear", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Compact(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/compact/start", map[string]any{
		"threadId": sess.ID,
	}, 10*time.Second)
	return err
}

func (c *Codex) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	return "", surface.ErrUnsupported
}

func (c *Codex) Interrupt(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "turn/interrupt", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Steer(ctx context.Context, sess *surface.Session, message string) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	// turn/steer only applies to a running turn. If idle, there is nothing to
	// steer into; the caller should use Send instead.
	turnID, _ := c.activeTurnID(ctx, conn, sess.ID)
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to steer (use 'send' instead)")
	}
	_, err = c.rpc(ctx, conn, "turn/steer", map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          []map[string]any{{"type": "text", "text": message}},
	}, 5*time.Second)
	return err
}

var localHTTPClient = &http.Client{Timeout: 5 * time.Second}

