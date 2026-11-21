package daemonapi

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

type G2Count struct {
	ProjectKey string    `json:"projectKey"`
	Since      time.Time `json:"since"`
	Checked    int       `json:"checked"`
	Gated      int       `json:"gated"`
}

type IdleCompactionHold struct {
	SessionID     string    `json:"sessionId"`
	Session       string    `json:"session"`
	Root          string    `json:"root"`
	RequestAt     time.Time `json:"requestAt"`
	ContextTokens int64     `json:"contextTokens"`
	Reason        string    `json:"reason"`
	At            time.Time `json:"at"`
}

// ClaudePermissionRequest is what `agenthail hook permission-request` posts:
// the hook's input as Claude passed it, the pid of the Claude process that
// ran the hook, and when it ran.
type ClaudePermissionRequest struct {
	Prompt claudesurface.ClaudePermissionPrompt `json:"prompt"`
	PID    int                                  `json:"pid"`
	// AskedAtMS is when the hook read the prompt, in Unix milliseconds.
	AskedAtMS int64 `json:"askedAt,omitempty"`
}

type ClaudePermissionResponse struct {
	Decision *claudesurface.ClaudePromptDecision `json:"decision"`
}

var DebugProfiles = []string{"heap", "allocs", "goroutine"}

const (
	DefaultCPUProfileSeconds = 30
	MaxCPUProfileSeconds     = 300
)
