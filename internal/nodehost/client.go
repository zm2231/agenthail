package nodehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
)

const maxResponseBytes = 1 << 20

var ErrNotRunning = errors.New("the host is not running")

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Hello struct {
	Protocol    int    `json:"protocol"`
	HostVersion string `json:"hostVersion"`
	PID         int    `json:"pid"`
}

type Client struct {
	Name   string
	Socket string
	Secret string
}

func (c Client) Hello(ctx context.Context) (Hello, error) {
	var hello Hello
	return hello, c.Call(ctx, "hello", nil, &hello)
}

func (c Client) Call(ctx context.Context, method string, params, result any) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return ErrNotRunning
		}
		return err
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })()
	if params == nil {
		params = struct{}{}
	}
	request, err := json.Marshal(map[string]any{"id": 1, "method": method, "params": params, "token": c.Secret})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return c.failed(ctx, method, err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxResponseBytes)).ReadBytes('\n')
	if err != nil {
		return c.failed(ctx, method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("%s %s: %w", c.Name, method, err)
	}
	if response.Error != nil {
		return response.Error
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}

func (c Client) failed(ctx context.Context, method string, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return fmt.Errorf("%s %s: %w", c.Name, method, err)
}
