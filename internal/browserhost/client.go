// Package browserhost runs the Node process that downloads and drives Chromium for agents.
package browserhost

import (
	"context"

	"github.com/zm2231/agenthail/internal/nodehost"
)

const Protocol = 2

var ErrNotRunning = nodehost.ErrNotRunning

type Error = nodehost.Error

type Hello = nodehost.Hello

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

type Client struct{ nodehost.Client }

func (c Client) Status(ctx context.Context) (Status, error) {
	var status Status
	return status, c.Call(ctx, "status", nil, &status)
}
