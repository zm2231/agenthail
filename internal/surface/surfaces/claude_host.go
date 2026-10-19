package surfaces

import (
	"context"
	"encoding/json"
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
	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/surface"
)

const claudeHostListTTL = time.Second

type claudeHost struct {
	client   claudehost.Client
	state    string
	mu       sync.Mutex
	listed   time.Time
	sessions map[string]int
}

// claudeHosted reports whether the host runs the session's conversation. Its
// process also serves Claude's native messaging socket, which agent messages
// take; only a person's turn goes through the host.
func claudeHosted(sess *surface.Session) bool {
	return sess != nil && sess.RuntimeKind == surface.RuntimeClaudeHost
}

func newClaudeHost(home string) *claudeHost {
	return &claudeHost{client: claudehost.NewClient(home), state: filepath.Join(claudehost.StateDir(home), "state.json")}
}

// runs reports whether a host started this Claude process for this
// conversation. That includes a process an earlier host started, which holds
// its session until it exits, so a person's turn to it waits rather than
// taking the native socket.
func (h *claudeHost) runs(ctx context.Context, pid int, conversation string) bool {
	return pid > 0 && conversation != "" && h.listing(ctx)[conversation] == pid
}

// forget drops the cached listing, so the next read asks the host.
func (h *claudeHost) forget() {
	h.mu.Lock()
	h.listed = time.Time{}
	h.mu.Unlock()
}

// listing is the host's sessions and orphans by conversation, with their
// process ids. A host that does not answer is read from its state file, which
// names the processes it started.
func (h *claudeHost) listing(ctx context.Context) map[string]int {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.listed) > claudeHostListTTL {
		listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		listing, err := h.client.List(listCtx)
		cancel()
		h.sessions = map[string]int{}
		if err == nil {
			for _, session := range listing.Sessions {
				h.sessions[session.SessionID] = session.PID
			}
			for _, orphan := range listing.Orphans {
				h.sessions[orphan.SessionID] = orphan.PID
			}
		} else {
			h.readState()
		}
		h.listed = time.Now()
	}
	return h.sessions
}

// readState reads the processes the host recorded starting. One counts only
// while its pid runs with the recorded start time; a different start time means
// another process reused the pid.
func (h *claudeHost) readState() {
	data, err := os.ReadFile(h.state)
	if err != nil {
		return
	}
	var state struct {
		Sessions map[string]struct {
			PID     int    `json:"pid"`
			Started string `json:"started"`
		} `json:"sessions"`
	}
	if json.Unmarshal(data, &state) != nil {
		return
	}
	for id, entry := range state.Sessions {
		if entry.Started == "" || !claudeProcessAlive(entry.PID) {
			continue
		}
		if started, err := claudepeer.ProcessStart(entry.PID); err == nil && started == entry.Started {
			h.sessions[id] = entry.PID
		}
	}
}

// The intent uuid becomes the transcript record's uuid: the turn is followed
// by it, and a resend of the same message runs once.
func (c *Claude) sendHostTurn(ctx context.Context, sess *surface.Session, message, priority string, images surface.Uploads) (*surface.SendResult, error) {
	id := surface.MessageUUID(ctx)
	if id == "" {
		id = uuid.NewString()
	}
	var attached []claudehost.Image
	for _, image := range images {
		attached = append(attached, claudehost.Image{Path: image.Path, MediaType: image.MediaType})
	}
	_, err := c.host.client.Send(ctx, claudehost.SendRequest{
		SessionID: sess.ID,
		UUID:      id,
		Text:      message,
		Images:    attached,
		Priority:  priority,
		Origin:    &claudehost.Origin{Kind: "human"},
	})
	if err != nil {
		return nil, claudeHostDeliveryError(err)
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

func (c *Claude) steerHost(ctx context.Context, sess *surface.Session, message string) error {
	_, err := c.sendHostTurn(ctx, sess, message, "now", nil)
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
		if err == nil {
			executable, err = filepath.EvalSymlinks(executable)
		}
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
