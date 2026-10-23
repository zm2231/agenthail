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
// RunAtLoad does, and print reports the pid launchd supervises for the loaded
// label and the plist it was loaded from. With orphan set, launchd reports another pid, as when it abandons a
// half-started instance that still runs.
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
	writeExecutable(t, filepath.Join(dir, "agenthail"), "#!/bin/sh\nmkdir -p \"$HOME/.agenthail\"\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n")
	writeExecutable(t, filepath.Join(dir, "launchctl"), `#!/bin/sh
dir="`+dir+`"
printf '%s\n' "$*" >> "$dir/calls"
start() {
	"$dir/agenthail" daemon-run >/dev/null 2>&1 &
	echo $! >> "$dir/pids"
	if [ -f "$dir/orphan" ]; then echo 1 > "$dir/service-pid"; else echo $! > "$dir/service-pid"; fi
}
stop() {
	[ -f "$dir/service-pid" ] || return 0
	pid="$(cat "$dir/service-pid")"
	[ "$pid" = 1 ] && return 0
	kill -TERM "$pid" 2>/dev/null
	while kill -0 "$pid" 2>/dev/null; do sleep 0.05; done
}
loaded() { [ -f "$dir/label" ] && [ "${1##*/}" = "$(cat "$dir/label")" ]; }
case "$1" in
print)
	loaded "$2" || exit 1
	echo "	path = $(cat "$dir/path")"
	echo "	pid = $(cat "$dir/service-pid")" ;;
bootstrap)
	echo com.agenthail.daemon > "$dir/label"
	echo "$3" > "$dir/path"
	start ;;
bootout)
	loaded "$2" || exit 0
	stop
	rm -f "$dir/label" "$dir/path" "$dir/service-pid" ;;
kickstart)
	loaded "$3" || exit 113
	stop
	start ;;
esac
exit 0
`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Cleanup(f.stop)
	return f
}

// supervise loads label from this HOME's LaunchAgents with a daemon already
// running under it.
func (f *fakeLaunchd) supervise(label string) int {
	f.t.Helper()
	cmd := exec.Command(filepath.Join(f.dir, "agenthail"), "daemon-run")
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	f.write("pids", strconv.Itoa(pid)+"\n")
	f.write("service-pid", strconv.Itoa(pid))
	f.write("label", label)
	f.write("path", launchAgentPath(label))
	if running, ok := waitForDaemonPID(0, 5*time.Second); !ok || running != pid {
		f.t.Fatalf("fake daemon = %d, %v; want %d", running, ok, pid)
	}
	return pid
}

func (f *fakeLaunchd) write(name, content string) {
	f.t.Helper()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if name == "pids" {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := os.OpenFile(filepath.Join(f.dir, name), flags, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeLaunchd) stop() {
	data, _ := os.ReadFile(filepath.Join(f.dir, "pids"))
	for _, field := range strings.Fields(string(data)) {
		if pid, err := strconv.Atoi(field); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func (f *fakeLaunchd) kickstarted() bool {
	return f.called("kickstart")
}

func (f *fakeLaunchd) called(command string) bool {
	f.t.Helper()
	calls, err := os.ReadFile(f.calls)
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return strings.Contains(string(calls), command)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchdInstallLetsRunAtLoadStartTheDaemon(t *testing.T) {
	fake := newFakeLaunchd(t)
	out, err := captureStdout(t, launchdService{}.Install)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if fake.kickstarted() {
		t.Fatal("install kickstarted the service it had just bootstrapped")
	}
	pid, running := daemon.IsRunning()
	if !running || !strings.Contains(out, "(pid "+strconv.Itoa(pid)+")") {
		t.Fatalf("install reported %q; running daemon %d, %v", out, pid, running)
	}
}

func TestLaunchdInstallFailsWhenLaunchdDoesNotRunTheDaemon(t *testing.T) {
	fake := newFakeLaunchd(t)
	fake.write("orphan", "")
	launchdReadyTimeout = 500 * time.Millisecond
	t.Cleanup(func() { launchdReadyTimeout = 15 * time.Second })
	out, err := captureStdout(t, launchdService{}.Install)
	if err == nil || !strings.Contains(err.Error(), daemon.LogFilePath()) {
		t.Fatalf("install of a daemon launchd does not run = %v\n%s", err, out)
	}
	if _, running := daemon.IsRunning(); !running {
		t.Fatal("the unsupervised daemon should be running for this case")
	}
}

func TestLaunchdRestartWaitsForTheDaemonLaunchdStarts(t *testing.T) {
	for _, label := range []string{daemonLaunchdLabel, homebrewDaemonLaunchdLabel} {
		t.Run(label, func(t *testing.T) {
			fake := newFakeLaunchd(t)
			before := fake.supervise(label)
			out, err := captureStdout(t, launchdService{}.Restart)
			if err != nil {
				t.Fatalf("restart: %v\n%s", err, out)
			}
			pid, running := daemon.IsRunning()
			service, loaded := launchdServiceJob(label)
			if !running || pid == before || !loaded || service.pid != pid || !strings.Contains(out, "(pid "+strconv.Itoa(pid)+")") {
				t.Fatalf("restart reported %q; daemon %d (was %d), launchd pid %d", out, pid, before, service.pid)
			}
		})
	}
}

// A command run with another HOME, such as a test or a scratch install, must
// manage that HOME's daemon and leave the user's launchd service running.
func TestLaunchdServiceOfAnotherHomeIsLeftAlone(t *testing.T) {
	for _, label := range []string{daemonLaunchdLabel, homebrewDaemonLaunchdLabel} {
		t.Run(label, func(t *testing.T) {
			fake := newFakeLaunchd(t)
			live := fake.supervise(label)
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
				t.Fatal(err)
			}
			unsupervisedDaemonExecutable = func() (string, error) { return filepath.Join(fake.dir, "agenthail"), nil }
			t.Cleanup(func() { unsupervisedDaemonExecutable = os.Executable })

			if (launchdService{}).Loaded() {
				t.Fatal("the other HOME's service counts as this HOME's")
			}
			out, err := captureStdout(t, launchdService{}.Restart)
			pid, running := daemon.IsRunning()
			if running {
				fake.write("pids", strconv.Itoa(pid)+"\n")
			}
			if err != nil || !running || pid == live {
				t.Fatalf("restart = %v, daemon %d, %v (service daemon %d)\n%s", err, pid, running, live, out)
			}
			if out, err := captureStdout(t, launchdService{}.Install); err == nil || !strings.Contains(err.Error(), launchAgentPath(label)) {
				t.Fatalf("install from another HOME = %v\n%s", err, out)
			}
			if label == daemonLaunchdLabel {
				if out, err := captureStdout(t, launchdService{}.Uninstall); err == nil {
					t.Fatalf("uninstall from another HOME succeeded\n%s", out)
				}
			}
			if fake.called("kickstart") || fake.called("bootout") || fake.called("bootstrap") {
				t.Fatal("a command run with another HOME changed the launchd service")
			}
			if err := syscall.Kill(live, 0); err != nil {
				t.Fatalf("service daemon %d stopped: %v", live, err)
			}
		})
	}
}

func TestParseLaunchdJobReadsOnlyTheJobsOwnFields(t *testing.T) {
	output := "gui/501/com.agenthail.daemon = {\n" +
		"\tactive count = 1\n" +
		"\tendpoints = {\n\t\tpath = /nested/endpoint\n\t\tpid = 7\n\t}\n" +
		"\tpath = /Users/a/Library/LaunchAgents/com.agenthail.daemon.plist\n" +
		"\tstdout path = /Users/a/.agenthail/daemon.log\n" +
		"\tpid = 52272\n" +
		"}\n"
	job := parseLaunchdJob(output)
	if job.path != "/Users/a/Library/LaunchAgents/com.agenthail.daemon.plist" || job.pid != 52272 {
		t.Fatalf("parsed %+v", job)
	}
}
