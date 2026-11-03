package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/testkit"
)

func shortenDaemonBounds(t *testing.T, start, gone time.Duration) {
	t.Helper()
	previousStart, previousGone := daemonStartBound, daemonGoneBound
	daemonStartBound, daemonGoneBound = start, gone
	t.Cleanup(func() { daemonStartBound, daemonGoneBound = previousStart, previousGone })
}

func scratchDaemonHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// startFakeDaemon runs a process that passes for the daemon and writes its pid file.
func startFakeDaemon(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "agenthail")
	testkit.WriteExecutable(t, bin, "#!/bin/sh\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	cmd := exec.Command(bin, "daemon-run")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	})
	if pid, ok := waitForDaemonPID(0, 5*time.Second); !ok || pid != cmd.Process.Pid {
		t.Fatalf("fake daemon = %d, %v; want %d", pid, ok, cmd.Process.Pid)
	}
	return cmd.Process.Pid
}

func TestALiveDaemonGetsLongerThanTheGoneBoundToStart(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 30*time.Second, 200*time.Millisecond)
	bin := filepath.Join(t.TempDir(), "agenthail")
	testkit.WriteExecutable(t, bin, "#!/bin/sh\nsleep 0.6\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	cmd := exec.Command(bin, "daemon-run")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	})
	pid, err := daemonStart{supervised: true, goneBound: daemonGoneBound, process: func() int { return cmd.Process.Pid }}.wait()
	if err != nil || pid != cmd.Process.Pid {
		t.Fatalf("a slow start that stays alive: pid %d, want %d, err %v", pid, cmd.Process.Pid, err)
	}
}

func TestADaemonThatIsGoneFailsAtTheGoneBoundNotTheStartBound(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 30*time.Second, 300*time.Millisecond)
	began := time.Now()
	_, err := daemonStart{goneBound: daemonGoneBound, process: func() int { return 0 }}.wait()
	if err == nil || !strings.Contains(err.Error(), "keeps exiting") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("took %s, want about the gone bound", took)
	}
}

func TestADaemonThatKeepsRespawningFailsOnItsThirdProcess(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 30*time.Second, 30*time.Second)
	next := 100
	_, err := daemonStart{goneBound: daemonGoneBound, process: func() int {
		next++
		return next
	}}.wait()
	if err == nil || !strings.Contains(err.Error(), "keeps exiting") {
		t.Fatalf("err = %v", err)
	}
}

func TestALiveDaemonThatNeverStartsFailsAtTheStartBound(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 400*time.Millisecond, 30*time.Second)
	_, err := daemonStart{goneBound: daemonGoneBound, process: func() int { return 101 }}.wait()
	if err == nil || !strings.Contains(err.Error(), "did not become ready") || !strings.Contains(err.Error(), daemon.LogFilePath()) {
		t.Fatalf("err = %v", err)
	}
}

func TestASpawnedDaemonThatExitedEndsTheWaitAtOnce(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 30*time.Second, 30*time.Second)
	began := time.Now()
	_, err := spawnedDaemonStart(101, func() bool { return true }).wait()
	if err == nil || time.Since(began) > 10*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(began))
	}
}

func TestReadinessWaitsForAnEnabledDashboardToAnswer(t *testing.T) {
	scratchDaemonHome(t)
	shortenDaemonBounds(t, 30*time.Second, 30*time.Second)
	var answering atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !answering.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Enabled = true
	config.Listen = server.Listener.Addr().(*net.TCPAddr).String()
	if err := daemon.SaveDashboardConfig(config); err != nil {
		t.Fatal(err)
	}
	want := startFakeDaemon(t)
	polled := 0
	pid, err := daemonStart{goneBound: daemonGoneBound, process: func() int {
		polled++
		if polled == 5 {
			answering.Store(true)
		}
		return want
	}}.wait()
	if err != nil || pid != want {
		t.Fatalf("pid %d, want %d, err %v", pid, want, err)
	}
	if polled < 5 {
		t.Fatalf("ready after %d polls, before the dashboard answered", polled)
	}
}

func TestLaunchdInstallWaitsPastTheGoneBoundForAStartThatIsMigrating(t *testing.T) {
	fake := newFakeLaunchd(t)
	shortenDaemonBounds(t, 30*time.Second, 200*time.Millisecond)
	testkit.WriteExecutable(t, filepath.Join(fake.dir, "agenthail"), "#!/bin/sh\nmkdir -p \"$HOME/.agenthail\"\nsleep 1\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	out, err := captureStdout(t, launchdService{}.Install)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	pid, running := daemon.IsRunning()
	if !running || !strings.Contains(out, "(pid "+strconv.Itoa(pid)+")") {
		t.Fatalf("install reported %q; daemon %d, %v", out, pid, running)
	}
}

func TestSystemdDoesNotCountADaemonItDidNotStart(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	shortenDaemonBounds(t, 400*time.Millisecond, 30*time.Second)
	fake.otherMainPID = 1
	for _, step := range []struct {
		name      string
		operation func() error
	}{
		{"install", service.Install},
		{"restart", service.Restart},
		{"start", func() error {
			if err := service.Stop(); err != nil {
				return err
			}
			return service.Start()
		}},
	} {
		if out, err := captureStdout(t, step.operation); err == nil || !strings.Contains(err.Error(), "did not become ready") {
			t.Fatalf("%s with another daemon = %v\n%s", step.name, err, out)
		}
	}
}

func TestSystemdRestartReportsADaemonThatKeepsExiting(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	shortenDaemonBounds(t, 30*time.Second, 300*time.Millisecond)
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	testkit.WriteExecutable(t, fake.daemonScript, "#!/bin/sh\nexit 1\n")
	fake.startFails = true
	_, err := captureStdout(t, service.Restart)
	if err == nil || !strings.Contains(err.Error(), "keeps exiting") {
		t.Fatalf("err = %v", err)
	}
}
