package surfaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const claudeHostListTTL = time.Second

type claudeHost struct {
	client claudehost.Client
	mu     sync.Mutex
	listed time.Time
	pids   map[int]string
}

// claudeNativeSocket reports whether a Claude transport delivers agent
// messages over the session's native messaging socket. A host session has
// that socket too; only a person's turn goes through the host.
func claudeNativeSocket(transport string) bool {
	return transport == "uds" || transport == "host"
}

func newClaudeHost(home string) *claudeHost {
	return &claudeHost{client: claudehost.NewClient(home)}
}

// runs reports whether the host runs this Claude process for this
// conversation. A host that does not answer runs nothing, so its sessions
// keep their native socket.
func (h *claudeHost) runs(ctx context.Context, pid int, conversation string) bool {
	if h == nil || pid <= 0 {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.listed) > claudeHostListTTL {
		listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		listing, err := h.client.List(listCtx)
		cancel()
		h.pids = map[int]string{}
		if err == nil {
			for _, session := range listing.Sessions {
				h.pids[session.PID] = session.SessionID
			}
		}
		h.listed = time.Now()
	}
	return h.pids[pid] == conversation
}

// The intent uuid becomes the transcript record's uuid: the turn is followed
// by it, and a resend of the same message runs once.
func (c *Claude) sendHostTurn(ctx context.Context, sess *surface.Session, message, priority string) (*surface.SendResult, error) {
	id := surface.MessageUUID(ctx)
	if id == "" {
		id = uuid.NewString()
	}
	_, err := c.host.client.Send(ctx, claudehost.SendRequest{
		SessionID: sess.ID,
		UUID:      id,
		Text:      message,
		Priority:  priority,
		Origin:    &claudehost.Origin{Kind: "human"},
	})
	if err != nil {
		return nil, claudeHostDeliveryError(err)
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

func (c *Claude) steerHost(ctx context.Context, sess *surface.Session, message string) error {
	_, err := c.sendHostTurn(ctx, sess, message, "now")
	return err
}

func (c *Claude) interruptHost(ctx context.Context, sess *surface.Session) error {
	if err := c.host.client.Interrupt(ctx, sess.ID); err != nil {
		return claudeHostDeliveryError(err)
	}
	return nil
}

func claudeHostDeliveryError(err error) error {
	var unknown *claudehost.OutcomeUnknownError
	switch {
	case errors.As(err, &unknown), claudehost.IsCode(err, "unknown"):
		return surface.DeliveryOutcomeUnknown(err)
	case errors.Is(err, claudehost.ErrNotRunning), claudehost.IsCode(err, "draining"), claudehost.IsCode(err, "held"):
		return surface.DeliveryUnavailable(fmt.Errorf("%w: %s", surface.ErrDeliveryHeld, err))
	case claudehost.IsCode(err, "invalid"):
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	default:
		return surface.DeliveryUnavailable(err)
	}
}

func ClaudeHostLaunch(home, version string) (claudehost.Launch, error) {
	script := strings.TrimSpace(os.Getenv("AGENTHAIL_CLAUDE_HOST"))
	if script == "" {
		executable, err := os.Executable()
		if err != nil {
			return claudehost.Launch{}, err
		}
		script = filepath.Join(filepath.Dir(executable), "claude-host", "host.mjs")
	}
	if _, err := os.Stat(script); err != nil {
		return claudehost.Launch{}, fmt.Errorf("the Claude host script is missing (set AGENTHAIL_CLAUDE_HOST): %w", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return claudehost.Launch{}, fmt.Errorf("the Claude host needs node on PATH: %w", err)
	}
	claude, err := surface.ClaudeBinary(home)
	if err != nil {
		return claudehost.Launch{}, err
	}
	return claudehost.Launch{Node: node, Script: script, Claude: claude, Version: version, Env: claudeCommandEnv(home)}, nil
}
