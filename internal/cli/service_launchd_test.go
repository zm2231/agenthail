package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

// fakeLaunchd stands in for launchctl. bootstrap starts the fake daemon, as
// RunAtLoad does, and print reports the pid launchd would supervise.
type fakeLaunchd struct {
	t     *testing.T
	dir   string
	calls string
}

func newFakeLaunchd(t *testing.T) *fakeLaunchd {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("launchd is only available on macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	dir := t.TempDir()
	f := &fakeLaunchd{t: t, dir: dir, calls: filepath.Join(dir, "calls")}
	daemonScript := filepath.Join(dir, "agenthail")
	writeExecutable(t, daemonScript, "#!/bin/sh\nmkdir -p \"$HOME/.agenthail\"\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	writeExecutable(t, filepath.Join(dir, "launchctl"), `#!/bin/sh
dir="`+dir+`"
printf '%s\n' "$*" >> "$dir/calls"
case "$1" in
print)
	case "$2" in
	*/com.agenthail.daemon) [ -f "$dir/service-pid" ] || exit 1; echo "	pid = $(cat "$dir/service-pid")" ;;
	*) exit 1 ;;
	esac ;;
bootstrap)
	"$dir/agenthail" daemon-run >/dev/null 2>&1 &
	echo $! > "$dir/service-pid" ;;
bootout)
	[ -f "$dir/service-pid" ] && kill -TERM "$(cat "$dir/service-pid")" 2>/dev/null
	rm -f "$dir/service-pid" ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Cleanup(f.stop)
	return f
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeLaunchd) stop() {
	if pid, ok := daemon.IsRunning(); ok {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

func (f *fakeLaunchd) report(pid int) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "service-pid"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func TestLaunchdInstallLetsRunAtLoadStartTheDaemon(t *testing.T) {
	fake := newFakeLaunchd(t)
	out, err := captureStdout(t, launchdService{}.Install)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	calls, err := os.ReadFile(fake.calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "kickstart") {
		t.Fatalf("install kickstarted the service it had just bootstrapped:\n%s", calls)
	}
	pid, running := daemon.IsRunning()
	if !running || !strings.Contains(out, "(pid "+strconv.Itoa(pid)+")") {
		t.Fatalf("install reported %q; running daemon %d, %v", out, pid, running)
	}
}

func TestADaemonLaunchdDoesNotSuperviseIsNotStarted(t *testing.T) {
	fake := newFakeLaunchd(t)
	cmd := exec.Command(filepath.Join(fake.dir, "agenthail"), "daemon-run")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if _, ok := waitForDaemonPID(0, 5*time.Second); !ok {
		t.Fatal("the fake daemon did not start")
	}

	fake.report(cmd.Process.Pid + 1)
	if pid, ok := waitForSupervisedDaemon(daemonLaunchdLabel, 0, 300*time.Millisecond); ok {
		t.Fatalf("daemon %d counted as started while launchd runs another pid", pid)
	}
	fake.report(cmd.Process.Pid)
	if pid, ok := waitForSupervisedDaemon(daemonLaunchdLabel, 0, time.Second); !ok || pid != cmd.Process.Pid {
		t.Fatalf("supervised daemon = %d, %v; want %d", pid, ok, cmd.Process.Pid)
	}
	if pid, ok := waitForSupervisedDaemon(daemonLaunchdLabel, cmd.Process.Pid, 300*time.Millisecond); ok {
		t.Fatalf("restart counted the daemon it replaced, %d", pid)
	}
}
