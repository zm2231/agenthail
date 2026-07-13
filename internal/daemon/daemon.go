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
	pollInterval = 5 * time.Second
)

type Daemon struct {
	Registry      *registry.Registry
	Surfaces      []surface.Surface
	log           *log.Logger
	errorMu       sync.Mutex
	observeErrors map[string]observedError
}

type observedError struct {
	message string
	at      time.Time
}

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
		Registry:      reg,
		Surfaces:      surfaces,
		log:           log.New(os.Stderr, "[daemon] ", log.LstdFlags),
		observeErrors: map[string]observedError{},
	}
}

func (d *Daemon) logObserveError(sessionID string, err error) {
	now := time.Now()
	message := err.Error()
	d.errorMu.Lock()
	previous, found := d.observeErrors[sessionID]
	if found && previous.message == message && now.Sub(previous.at) < time.Minute {
		d.errorMu.Unlock()
		return
	}
	d.observeErrors[sessionID] = observedError{message: message, at: now}
	d.errorMu.Unlock()
	d.log.Printf("observe %s: %s", d.resolveDisplay(sessionID), err)
}

func (d *Daemon) clearObserveError(sessionID string) {
	d.errorMu.Lock()
	delete(d.observeErrors, sessionID)
	d.errorMu.Unlock()
}

func (d *Daemon) logRuntimeError(key string, err error) {
	now := time.Now()
	message := err.Error()
	d.errorMu.Lock()
	previous, found := d.observeErrors[key]
	if found && previous.message == message && now.Sub(previous.at) < time.Minute {
		d.errorMu.Unlock()
		return
	}
	d.observeErrors[key] = observedError{message: message, at: now}
	d.errorMu.Unlock()
	d.log.Printf("%s: %s", key, err)
}

func (d *Daemon) Run(ctx context.Context) error {
	d.log.Printf("started; %d surfaces", len(d.Surfaces))
	if recovered, err := d.Registry.RecoverInflight(time.Now().Add(-time.Minute)); err != nil {
		d.log.Printf("warn: recover inflight queue: %s", err)
	} else if recovered > 0 {
		d.log.Printf("dead-lettered %d message(s) with an uncertain delivery outcome", recovered)
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	d.scanAndRelay(ctx)
	for {
		select {
		case <-ctx.Done():
			d.log.Printf("stopping")
			return nil
		case <-ticker.C:
			d.scanAndRelay(ctx)
		}
	}
}

func (d *Daemon) RunWithSignal() error {
	lock, err := acquireDaemonLock()
	if err != nil {
		return err
	}
	defer releaseDaemonLock(lock)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	d.log.Printf("daemon pid %d", os.Getpid())
	if err := os.WriteFile(PidFilePath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer func() {
		if removeErr := removePIDFileIfOwned(os.Getpid()); removeErr != nil {
			d.log.Printf("warn: remove pidfile: %s", removeErr)
		}
	}()
	dashboard, err := d.startDashboard()
	if err != nil {
		return err
	}
	if dashboard != nil {
		defer func() {
			if shutdownErr := dashboard.shutdown(); shutdownErr != nil {
				d.log.Printf("dashboard shutdown: %s", shutdownErr)
			}
		}()
	}
	return d.Run(ctx)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

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
	if err := exec.Command("kill", "-0", fmt.Sprintf("%d", pid)).Run(); err != nil || !processIsDaemon(pid) {
		return 0, false
	}
	return pid, true
}

func Stop() error {
	pid, ok := IsRunning()
	if !ok {
		return fmt.Errorf("daemon not running")
	}
	if err := exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run(); err != nil {
		return err
	}
	if !waitForStopped(pid, 5*time.Second) {
		return fmt.Errorf("daemon pid %d did not stop within 5s", pid)
	}
	return nil
}
