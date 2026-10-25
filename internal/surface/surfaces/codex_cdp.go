package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

type cdpConn struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	next   int
	target string
}

func (c *Codex) dial(ctx context.Context) (*cdpConn, error) {
	if c.desktopURL == "" {
		return nil, fmt.Errorf("Codex Desktop does not run on %s; set AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT to reach one", runtime.GOOS)
	}
	targets, err := surface.ProbeOnce(ctx, codexDesktopProbe{c.desktopURL}, func() ([]codexCDPTarget, error) {
		return resolveCodexRendererEndpoint(ctx, c.desktopURL)
	})
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	var failures []string
	for _, target := range targets {
		ws, _, dialErr := d.DialContext(ctx, target.wsURL, http.Header{})
		if dialErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", target.wsURL, dialErr))
			continue
		}
		conn := &cdpConn{ws: ws, next: 1, target: target.wsURL}
		if probeErr := c.ensureDesktopHook(ctx, conn); probeErr == nil {
			return conn, nil
		} else {
			failures = append(failures, fmt.Sprintf("%s: Desktop app-server bridge unavailable (%v)", target.wsURL, probeErr))
		}
		_ = conn.close()
	}
	return nil, fmt.Errorf("connect Codex Desktop renderer bridge: %s", strings.Join(failures, "; "))
}

type codexDesktopProbe struct{ endpoint string }

type codexCDPTarget struct {
	wsURL string
}

func resolveCodexRendererEndpoint(ctx context.Context, endpoint string) ([]codexCDPTarget, error) {
	httpURL := strings.Replace(endpoint, "ws://", "http://", 1)
	httpURL = strings.Replace(httpURL, "wss://", "https://", 1)
	for _, path := range []string{"/json/list", "/json"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", httpURL+path, nil)
		resp, err := localHTTPClient.Do(req)
		if err != nil {
			continue
		}
		var values []map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&values)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil {
			continue
		}
		var primary []codexCDPTarget
		var secondary []codexCDPTarget
		for _, value := range values {
			if kind, _ := value["type"].(string); kind != "page" {
				continue
			}
			url, _ := value["url"].(string)
			wsURL, _ := value["webSocketDebuggerUrl"].(string)
			if wsURL == "" || !strings.HasPrefix(url, "app://-/index.html") {
				continue
			}
			target := codexCDPTarget{wsURL: wsURL}
			if strings.Contains(url, "avatar-overlay") {
				secondary = append(secondary, target)
			} else {
				primary = append(primary, target)
			}
		}
		if len(primary) > 0 {
			return append(primary, secondary...), nil
		}
	}
	return nil, fmt.Errorf("no Codex Desktop renderer debug target at %s; quit Codex and relaunch it with 'agenthail launch codex'", endpoint)
}

func (c *cdpConn) close() error { return c.ws.Close() }

func (c *cdpConn) evaluate(ctx context.Context, expr string, timeout time.Duration) (any, error) {
	c.mu.Lock()
	id := c.next
	c.next++
	c.mu.Unlock()

	req := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expr,
			"awaitPromise":  true,
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
		msgID, _ := msg["id"].(float64)
		if int(msgID) != id {
			continue
		}
		result, _ := msg["result"].(map[string]any)
		if details, _ := result["exceptionDetails"].(map[string]any); details != nil {
			text, _ := details["text"].(string)
			if exception, _ := details["exception"].(map[string]any); exception != nil {
				if description, _ := exception["description"].(string); description != "" {
					text = description
				}
			}
			return nil, fmt.Errorf("eval error: %s", text)
		}
		res, _ := result["result"].(map[string]any)
		if sub, _ := res["subtype"].(string); sub == "error" {
			desc, _ := res["description"].(string)
			return nil, fmt.Errorf("eval error: %s", desc)
		}
		return res["value"], nil
	}
	return nil, fmt.Errorf("timeout waiting for eval response: %w", os.ErrDeadlineExceeded)
}

var localHTTPClient = &http.Client{Timeout: 5 * time.Second}
