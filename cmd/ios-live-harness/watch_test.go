package main

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
)

const helperEnv = "IOS_LIVE_HARNESS_HELPER"

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == stubClaudeName {
		os.Exit(runStubClaude(os.Args[1:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "daemon-run" {
		fakeDaemon()
		return
	}
	if mode := os.Getenv(helperEnv); mode != "" {
		helper(mode)
		return
	}
	os.Exit(testkit.RunInOwnHome(m.Run))
}

// held keeps the helper's descriptors open until it is killed.
var held []any

// helper stands in for a host daemon. It does what the mode names, prints
// "ready" and waits to be killed.
func helper(mode string) {
	switch mode {
	case "inside":
		listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
		must(err)
		unix, err := net.Listen("unix", filepath.Join(os.Getenv("ROOT"), "own.sock"))
		must(err)
		file, err := os.Create(filepath.Join(os.Getenv("ROOT"), "own.txt"))
		must(err)
		held = append(held, listener, unix, file)
	case "outside":
		tcp, err := net.Dial("tcp", os.Getenv("TCP"))
		must(err)
		unix, err := net.Dial("unix", os.Getenv("UNIX"))
		must(err)
		file, err := os.Open(os.Getenv("FILE"))
		must(err)
		held = append(held, tcp, unix, file)
	case "planted":
		listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
		must(err)
		tcp, err := net.Dial("tcp", os.Getenv("TCP"))
		must(err)
		held = append(held, listener, tcp)
	case "exec":
		child := exec.Command(os.Getenv("PROGRAM"), "60")
		must(child.Start())
		held = append(held, child)
	}
	os.Stdout.WriteString("ready\n")
	// A bare select{} is a deadlock to the runtime once no goroutine can
	// wake it, which on macOS kills the helper; a signal channel is not.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	<-stop
	for _, h := range held {
		if child, ok := h.(*exec.Cmd); ok {
			child.Process.Kill()
			child.Wait()
		}
	}
}

func must(err error) {
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}

// shortTemp returns a directory short enough for Unix socket paths.
func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ihw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func startHelper(t *testing.T, mode string, env ...string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), append([]string{helperEnv + "=" + mode}, env...)...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("helper %s did not start: %q %v", mode, line, err)
	}
	return cmd.Process.Pid
}

// helperProgram is the helper's own executable, which every scope in these
// tests allows.
func helperProgram(t *testing.T) map[string]bool {
	t.Helper()
	path, err := filepath.EvalSymlinks(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if path, err = filepath.Abs(path); err != nil {
		t.Fatal(err)
	}
	return map[string]bool{path: true}
}

func watchOnce(t *testing.T, pid int, s scope) []string {
	t.Helper()
	if s.executables == nil {
		s.executables = helperProgram(t)
	}
	w := newWatcher(newBackend(), 50*time.Millisecond, func() map[int]scope { return map[int]scope{pid: s} })
	w.start()
	time.Sleep(300 * time.Millisecond)
	w.halt()
	// An exited helper stays a zombie until the test reaps it, so ask ps.
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		t.Fatalf("helper %d exited before the watcher finished reading it: %q %v", pid, state, err)
	}
	return w.violations()
}

func TestAHostThatStaysInsideItsScratchDirectoryAndPortPasses(t *testing.T) {
	root := shortTemp(t)
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	pid := startHelper(t, "inside", "ROOT="+root, "PORT="+strconv.Itoa(port))
	if found := watchOnce(t, pid, scope{name: "host", root: root, port: port, forbidden: []string{filepath.Dir(root)}}); len(found) != 0 {
		t.Fatalf("violations for a host that stayed inside its scope: %q", found)
	}
}

func TestTheWatcherFlagsALoopbackPeerASocketAndAFileOutsideTheHostsScope(t *testing.T) {
	root := shortTemp(t)
	elsewhere := shortTemp(t)
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	go acceptAll(other)
	shared, err := net.Listen("unix", filepath.Join(elsewhere, "shared.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	go acceptAll(shared)
	private := filepath.Join(elsewhere, "private.txt")
	if err := os.WriteFile(private, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	pid := startHelper(t, "outside", "TCP="+other.Addr().String(), "UNIX="+filepath.Join(elsewhere, "shared.sock"), "FILE="+private)

	found := strings.Join(watchOnce(t, pid, scope{name: "host", root: root, port: port, forbidden: []string{elsewhere}}), "\n")
	for _, want := range []string{
		"has a TCP peer on a port the harness did not record",
		"uses a Unix socket outside its scratch directory",
		"opened a file in " + elsewhere,
	} {
		if !strings.Contains(found, want) {
			t.Errorf("missing %q in:\n%s", want, found)
		}
	}
}

func acceptAll(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
	}
}

func TestTheWatcherFlagsAChildRunningAProgramTheHarnessDidNotAllow(t *testing.T) {
	root := shortTemp(t)
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	pid := startHelper(t, "exec", "PROGRAM="+sleep)
	s := scope{name: "host", root: root, forbidden: []string{root + "-none"}}
	found := strings.Join(watchOnce(t, pid, s), "\n")
	if !strings.Contains(found, "runs a program the harness did not put on its PATH") {
		t.Fatalf("a child running %s was not flagged:\n%s", sleep, found)
	}

	allowed := helperProgram(t)
	resolved, err := filepath.EvalSymlinks(sleep)
	if err != nil {
		t.Fatal(err)
	}
	allowed[resolved] = true
	s.executables = allowed
	if found := watchOnce(t, pid, s); len(found) != 0 {
		t.Fatalf("a child running an allowed program was flagged: %q", found)
	}
}
