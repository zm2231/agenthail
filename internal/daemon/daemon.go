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
	log            *log.Logger
}

func New(reg *registry.Registry, surfaces []surface.Surface) *Daemon {
	return &Daemon{
		Registry:      reg,
		Surfaces:      surfaces,
		lastReply:     map[string]string{},
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
			d.checkReplyDelta(ctx, s, &ptr)
		}
	}
}

// checkReplyDelta polls the latest reply text for a session. If it changed since
// the last scan, the turn is treated as complete: relays fire and the steer queue drains.
// This is surface-agnostic (works whether the surface reports busy/idle status or not).
func (d *Daemon) checkReplyDelta(ctx context.Context, surf surface.Surface, sess *surface.Session) {
	d.onTurnComplete(ctx, surf, sess)
}

func (d *Daemon) onTurnComplete(ctx context.Context, surf surface.Surface, sess *surface.Session) {
	reply, err := surf.Reply(ctx, sess, 1)
	if err == nil && reply != nil {
		d.mu.Lock()
		prev := d.lastReply[sess.ID]
		d.lastReply[sess.ID] = reply.Text
		d.mu.Unlock()
		if reply.Text != "" && reply.Text != prev {
			d.fireRelays(ctx, sess, reply.Text)
		}
	}
	d.drainMessageQueue(ctx, surf, sess)
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
		payload := fmt.Sprintf("[relay from %s %s] %s", from.Surface, truncate(from.ID, 8), text)
		d.log.Printf("relay %s -> %s", truncate(from.ID, 12), truncate(toSess.ID, 12))
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
