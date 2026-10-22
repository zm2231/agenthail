package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// codexStdioClient runs its own `codex app-server` over stdio for one task,
// with the environment it is given, so it never reaches a thread or a daemon
// another process runs. It and everything it starts live no longer than the
// context it was started with or the Close that ends it.
type codexStdioClient struct {
	ctx     context.Context
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	next    int
	waiting map[int]chan map[string]any
	done    chan struct{}
}

func startCodexStdio(ctx context.Context, binary string, env []string) (*codexStdioClient, error) {
	// Plugins are off because at startup Codex syncs its plugin catalog with a
	// git clone into CODEX_HOME, which this short run would leave half done.
	cmd := processGroupCommand(ctx, binary, "app-server", "--disable", "plugins")
	cmd.Env = env
	cmd.Stderr = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	c := &codexStdioClient{ctx: ctx, cmd: cmd, stdin: stdin, waiting: map[int]chan map[string]any{}, done: make(chan struct{})}
	go c.read(stdout)
	if _, err := c.Request(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "agenthail", "version": "0"}}, 30*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *codexStdioClient) read(stdout io.Reader) {
	defer close(c.done)
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 1<<20), 1<<26)
	for lines.Scan() {
		var message map[string]any
		if json.Unmarshal(lines.Bytes(), &message) != nil {
			continue
		}
		if _, notification := message["method"]; notification {
			continue
		}
		id, ok := message["id"].(float64)
		if !ok {
			continue
		}
		c.mu.Lock()
		reply := c.waiting[int(id)]
		delete(c.waiting, int(id))
		c.mu.Unlock()
		if reply != nil {
			reply <- message
		}
	}
}

func (c *codexStdioClient) write(message map[string]any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *codexStdioClient) Request(ctx context.Context, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	reply := make(chan map[string]any, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.waiting[id] = reply
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case message := <-reply:
		if failure, ok := message["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%s: %v", method, failure["message"])
		}
		return message, nil
	case <-c.done:
		return nil, errors.New("codex app-server exited")
	case <-timer.C:
		return nil, fmt.Errorf("%s: no answer from codex app-server", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *codexStdioClient) Close() error {
	c.stdin.Close()
	select {
	case <-c.done:
	case <-c.ctx.Done():
	case <-time.After(5 * time.Second):
	}
	// Killing the group also ends anything the app-server started, which
	// could otherwise keep writing to CODEX_HOME after it exits.
	_ = c.cmd.Cancel()
	return c.cmd.Wait()
}
