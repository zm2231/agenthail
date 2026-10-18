package codextest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Lifecycle is a fake `codex` CLI installed where Codex's daemon keeps its
// own package. `app-server daemon start` brings up a fake Daemon that keeps
// the threads of the one before it, unloaded, as a restarted daemon does.
type Lifecycle struct {
	Home    string
	logPath string
	marker  string
	respond Responder
	mu      sync.Mutex
	daemon  *Daemon
}

func InstallLifecycle(t testing.TB, home string, respond Responder) *Lifecycle {
	t.Helper()
	lifecycle := &Lifecycle{Home: home, logPath: filepath.Join(home, "lifecycle.log"), marker: filepath.Join(home, "start-requested"), respond: respond}
	cli := filepath.Join(home, "packages", "app-server-daemon", "current", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
[ "$1 $2 $3" = "app-server daemon start" ] || exit 2
: > %q
tries=0
while [ -e %q ]; do
	sleep 0.02
	tries=$((tries + 1))
	[ "$tries" -lt 500 ] || exit 1
done
printf '%%s\n' '{"status":"started","backend":"pid"}'
`, lifecycle.logPath, lifecycle.marker, lifecycle.marker)
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			if _, err := os.Stat(lifecycle.marker); err != nil {
				continue
			}
			lifecycle.Start(t)
			_ = os.Remove(lifecycle.marker)
		}
	}()
	return lifecycle
}

// Start brings the daemon up, carrying over the previous daemon's threads;
// a turn that was running reads back as interrupted, as it does in Codex.
func (l *Lifecycle) Start(t testing.TB) *Daemon {
	l.mu.Lock()
	defer l.mu.Unlock()
	previous := l.daemon
	daemon := Serve(t, l.Home, l.respond)
	if daemon == nil {
		return nil
	}
	if previous != nil {
		daemon.mu.Lock()
		defer daemon.mu.Unlock()
		previous.mu.Lock()
		for _, id := range previous.order {
			thread := *previous.threads[id]
			thread.Turns = append([]Turn(nil), thread.Turns...)
			for index := range thread.Turns {
				if thread.Turns[index].Status == "inProgress" {
					thread.Turns[index].Status = "interrupted"
				}
			}
			daemon.threads[id] = &thread
			daemon.order = append(daemon.order, id)
		}
		daemon.next = previous.next
		previous.mu.Unlock()
	}
	l.daemon = daemon
	return daemon
}

// Crash stops the daemon and leaves its socket file behind.
func (l *Lifecycle) Crash() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.daemon != nil {
		l.daemon.Stop()
	}
}

func (l *Lifecycle) Daemon() *Daemon {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.daemon
}

// Commands lists every lifecycle command Agenthail ran.
func (l *Lifecycle) Commands() []string {
	data, err := os.ReadFile(l.logPath)
	if err != nil {
		return nil
	}
	var commands []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			commands = append(commands, line)
		}
	}
	return commands
}
