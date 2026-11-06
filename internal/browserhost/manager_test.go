package browserhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/testnode"
)

func repoLaunch(t *testing.T) Launch {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "..", "..", "browser-host", "host.mjs")
	return Launch{Node: testnode.Path(t), Script: script, Version: "test", Env: os.Environ()}
}

func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "bh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

func fastRestarts(t *testing.T) {
	t.Helper()
	previous := minRestart
	minRestart = 10 * time.Millisecond
	t.Cleanup(func() { minRestart = previous })
}

func running(manager *Manager, t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func awaitHost(t *testing.T, manager *Manager, other int) Hello {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var hello Hello
	err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		observed, err := manager.Client().Hello(ctx)
		if err != nil || observed.PID == other || manager.Status().State != StateAbsent {
			return false, nil
		}
		hello = observed
		return true, nil
	})
	if err != nil {
		t.Fatalf("no browser host answered with a status: %v (status %+v)", err, manager.Status())
	}
	return hello
}

func gone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestTheDaemonRestartsAKilledBrowserHostAndStopsItOnExit(t *testing.T) {
	fastRestarts(t)
	launch := repoLaunch(t)
	manager := &Manager{Home: shortHome(t), Launch: func() (Launch, error) { return launch, nil }}
	stop := running(manager, t)
	first := awaitHost(t, manager, 0)
	if first.Protocol != Protocol || first.HostVersion != "test" {
		t.Fatalf("hello = %+v", first)
	}
	if err := syscall.Kill(first.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	second := awaitHost(t, manager, first.PID)
	stop()
	deadline := time.Now().Add(10 * time.Second)
	for !gone(second.PID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !gone(second.PID) {
		t.Fatalf("browser host %d still runs after the daemon stopped", second.PID)
	}
	if _, err := os.Stat(SocketPath(manager.Home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

const silentStatusHost = `
import net from 'node:net';
import readline from 'node:readline';
const socket = process.argv[process.argv.indexOf('--socket') + 1];
const version = process.argv[process.argv.indexOf('--version') + 1];
net.createServer(connection => {
  readline.createInterface({ input: connection }).on('line', line => {
    if (JSON.parse(line).method === 'hello') {
      connection.write(JSON.stringify({ id: 1, result: { protocol: 1, hostVersion: version, pid: process.pid } }) + '\n');
    }
  });
}).listen(socket);
process.stdin.on('close', () => process.exit(0));
process.stdin.resume();
`

func TestTheDaemonStopsPromptlyWhenTheBrowserHostNeverAnswersStatus(t *testing.T) {
	launch := repoLaunch(t)
	launch.Script = filepath.Join(t.TempDir(), "silent.mjs")
	if err := os.WriteFile(launch.Script, []byte(silentStatusHost), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{Home: shortHome(t), Launch: func() (Launch, error) { return launch, nil }}
	stop := running(manager, t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		_, err := manager.Client().Hello(ctx)
		return err == nil, nil
	}); err != nil {
		t.Fatalf("the fake host never answered hello: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the daemon did not stop while the host left status unanswered")
	}
}

func TestABrowserHostThatCannotStartIsReportedUnavailableAndRetried(t *testing.T) {
	fastRestarts(t)
	launch := repoLaunch(t)
	var attempts atomic.Int32
	manager := &Manager{Home: shortHome(t), Launch: func() (Launch, error) {
		if attempts.Add(1) <= 2 {
			return Launch{}, errors.New("the browser host script is missing")
		}
		return launch, nil
	}}
	if status := manager.Status(); status.State != StateUnavailable {
		t.Fatalf("status before start = %+v", status)
	}
	stop := running(manager, t)
	defer stop()
	awaitHost(t, manager, 0)
	if attempts.Load() < 3 {
		t.Fatalf("started after %d attempts", attempts.Load())
	}
}
