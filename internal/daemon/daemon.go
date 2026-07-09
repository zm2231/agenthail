package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	pollInterval   = 5 * time.Second
)

type Daemon struct {
	Registry       *registry.Registry
	Surfaces       []surface.Surface
	mu             sync.Mutex
	lastReply      map[string]string
	lastGoalStatus map[string]string // sessionID -> last known goal status
	initialized    bool // false on first scan — suppress relays until baseline set
	log            *log.Logger
}

// resolveDisplay returns a human-readable label for a session ID.
func (d *Daemon) resolveDisplay(sessionID string) string {
	if d.Registry != nil {
		if alias, err := d.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
			return "@" + alias
		}
		if sfc, name, _, err := d.Registry.GetSession(sessionID); err == nil {
			if name != "" {
				return sfc + "/" + truncate(name, 20)
			}
		}
	}
	return truncate(sessionID, 24)
}

func New(reg *registry.Registry, surfaces []surface.Surface) *Daemon {
	return &Daemon{
		Registry:       reg,
		Surfaces:      surfaces,
		lastReply:     map[string]string{},
		lastGoalStatus: map[string]string{},
		log:           log.New(os.Stderr, "[daemon] ", log.LstdFlags),
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	d.log.Printf("started; %d surfaces", len(d.Surfaces))
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	d.scanAndRelay(ctx)
	for {
		select {
		case <-ctx.Done():
			d.log.Printf("stopping")
			return ctx.Err()
		case <-ticker.C:
			d.scanAndRelay(ctx)
		}
	}
}

func (d *Daemon) RunWithSignal() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	d.log.Printf("daemon pid %d", os.Getpid())
	if err := os.WriteFile(PidFilePath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		d.log.Printf("warn: write pidfile: %s", err)
	}
	return d.Run(ctx)
}

func (d *Daemon) scanAndRelay(ctx context.Context) {
	for _, s := range d.Surfaces {
		sessions, err := s.List(ctx)
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			d.Registry.RegisterSession(sess)
			var ptr surface.Session = sess
			d.onTurnComplete(ctx, s, &ptr)
		}
	}
	// Mark as initialized after first full scan (baseline established)
	d.mu.Lock()
	d.initialized = true
	d.mu.Unlock()
}

// onTurnComplete checks if a turn has completed and fires relays accordingly.
// Relay gating logic:
// 1. First scan (initialized=false): establish baseline — record reply + goal
//    state, DON'T fire any relays. Prevents backlog replay on daemon restart.
// 2. If surface supports goals (GoalGet returns non-nil):
//    - Only fire relay when goal status transitions to "complete"
//    - Intermediate turn ends (goal still "active") are suppressed
// 3. If surface doesn't support goals (GoalGet returns nil):
//    - Fall back to reply-delta: fire when reply text changes
//    - This is the legacy behavior for Claude/Notion
func (d *Daemon) onTurnComplete(ctx context.Context, surf surface.Surface, sess *surface.Session) {
	reply, err := surf.Reply(ctx, sess, 1)
	if err != nil || reply == nil {
		d.drainMessageQueue(ctx, surf, sess)
		return
	}

	// Check goal state
	goal, _ := surf.GoalGet(ctx, sess)
	goalStatus := ""
	if goal != nil {
		goalStatus = goal.Status
	}

	d.mu.Lock()
	prevReply := d.lastReply[sess.ID]
	prevGoal := d.lastGoalStatus[sess.ID]
	firstScan := !d.initialized

	d.lastReply[sess.ID] = reply.Text
	d.lastGoalStatus[sess.ID] = goalStatus
	d.mu.Unlock()

	// First scan: just record baseline, don't relay anything
	if firstScan {
		return
	}

	// Goal-gated relay (Codex): fire only when goal transitions to "complete"
	if goal != nil {
		if goalStatus == "complete" && prevGoal != "complete" {
			d.fireRelays(ctx, sess, reply.Text)
		}
		// If goal is active, suppress relay even if reply changed — intermediate turn
		return
	}

	// Fallback: reply-delta relay (Claude/Notion — no goal API)
	if reply.Text != "" && reply.Text != prevReply {
		d.fireRelays(ctx, sess, reply.Text)
	}
}

func (d *Daemon) fireRelays(ctx context.Context, from *surface.Session, text string) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return
	}
	for _, r := range routes {
		if r.FromSession != from.ID {
			continue
		}
		if !matchPattern(r.Pattern, text) {
			continue
		}
		toSess, toSurf := d.findSession(ctx, r.ToSession)
		if toSess == nil {
			d.log.Printf("relay target %s not found", truncate(r.ToSession, 16))
			continue
		}
		payload := fmt.Sprintf("[relay from %s %s] %s", from.Surface, d.resolveDisplay(from.ID), text)
		d.log.Printf("relay %s -> %s", d.resolveDisplay(from.ID), d.resolveDisplay(toSess.ID))
		if _, err := toSurf.Send(ctx, toSess, payload); err != nil {
			d.log.Printf("relay send failed: %s", err)
		}
	}
}

func (d *Daemon) drainMessageQueue(ctx context.Context, surf surface.Surface, sess *surface.Session) {
	ids, msgs, err := d.Registry.GetMessageQueue(sess.ID)
	if err != nil || len(ids) == 0 {
		return
	}
	d.log.Printf("draining %d queued message(s) for %s", len(ids), truncate(sess.ID, 16))
	for i, msg := range msgs {
		// Queued messages start a new turn (the prior turn already completed).
		// Send auto-queues again if somehow still busy.
		if _, err := surf.Send(ctx, sess, msg); err != nil {
			d.log.Printf("queue drain send failed: %s", err)
			continue
		}
		d.Registry.MarkMessageDelivered(ids[i])
	}
}

func (d *Daemon) findSession(ctx context.Context, id string) (*surface.Session, surface.Surface) {
	for _, s := range d.Surfaces {
		if sess, err := s.Resolve(ctx, id); err == nil {
			return sess, s
		}
	}
	return nil, nil
}

func matchPattern(pattern, text string) bool {
	if pattern == "" || pattern == ".*" {
		return true
	}
	matched, err := filepath.Match(pattern, text)
	if err != nil {
		return false
	}
	return matched
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// daemon control: spawn/stop/status via a pidfile in ~/.agenthail/
func PidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.pid")
}

func LogFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.log")
}

func IsRunning() (int, bool) {
	data, err := os.ReadFile(PidFilePath())
	if err != nil {
		return 0, false
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	if pid <= 0 {
		return 0, false
	}
	if err := exec.Command("kill", "-0", fmt.Sprintf("%d", pid)).Run(); err != nil {
		return 0, false
	}
	return pid, true
}

func Stop() error {
	pid, ok := IsRunning()
	if !ok {
		return fmt.Errorf("daemon not running")
	}
	os.Remove(PidFilePath())
	return exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run()
}
