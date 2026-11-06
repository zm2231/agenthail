// Package browserhost runs the Node process that downloads and drives Chromium for agents.
package browserhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
)

const Protocol = 1

const maxResponseBytes = 1 << 20

var ErrNotRunning = errors.New("the browser host is not running")

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

type Status struct {
	State   string   `json:"state"`
	Version string   `json:"version,omitempty"`
	Running bool     `json:"running"`
	Missing []string `json:"missing,omitempty"`
	Install string   `json:"install,omitempty"`
	Error   string   `json:"error,omitempty"`
}

const (
	StateAbsent           = "absent"
	StateReady            = "ready"
	StateMissingLibraries = "missing_libraries"
	StateFailed           = "failed"
	StateUnavailable      = "unavailable"
)

type Client struct{ Socket string }

func (c Client) Hello(ctx context.Context) (Hello, error) {
	var hello Hello
	return hello, c.call(ctx, "hello", &hello)
}

func (c Client) Status(ctx context.Context) (Status, error) {
	var status Status
	return status, c.call(ctx, "status", &status)
}

func (c Client) Launch(ctx context.Context) (Status, error) {
	var status Status
	return status, c.call(ctx, "launch", &status)
}

func (c Client) Close(ctx context.Context) (Status, error) {
	var status Status
	return status, c.call(ctx, "close", &status)
}

func (c Client) call(ctx context.Context, method string, result any) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return ErrNotRunning
		}
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request, err := json.Marshal(map[string]any{"id": 1, "method": method})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("browser host %s: %w", method, err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxResponseBytes)).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("browser host %s: %w", method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("browser host %s: %w", method, err)
	}
	if response.Error != nil {
		return response.Error
	}
	return json.Unmarshal(response.Result, result)
}
