package main

import (
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// endpoint is one open descriptor of a scratch host process, read from the
// operating system.
type endpoint struct {
	pid  int
	kind string // tcp, udp, unix, file or exe
	// For tcp and udp.
	local, remote netip.AddrPort
	listening     bool
	// For unix, the socket's own bound path and, when it is connected, the
	// bound path of its peer. For file, the path. For exe, the process's
	// executable.
	path, peer string
}

func (e endpoint) String() string {
	switch e.kind {
	case "tcp", "udp":
		if e.listening {
			return fmt.Sprintf("pid %d %s listening on %s", e.pid, e.kind, e.local)
		}
		return fmt.Sprintf("pid %d %s %s -> %s", e.pid, e.kind, e.local, e.remote)
	case "unix":
		return fmt.Sprintf("pid %d unix %q peer %q", e.pid, e.path, e.peer)
	default:
		return fmt.Sprintf("pid %d %s %q", e.pid, e.kind, e.path)
	}
}

// scope is what one scratch host may touch.
type scope struct {
	name string
	root string
	port int
	// trap is the harness's own port that the host's Codex debugging port
	// and push relay point at.
	trap int
	// forbidden are directories no host process may open anything under:
	// the person's own HOME and the shared Claude socket directory.
	forbidden []string
	// executables and executableDirs are the only programs a host process
	// may run.
	executables    map[string]bool
	executableDirs []string
}

// check returns why an endpoint breaks the host's isolation, or "".
func (s scope) check(e endpoint) string {
	switch e.kind {
	case "tcp":
		if e.listening {
			if e.local.Port() == uint16(s.port) {
				return ""
			}
			return "listens on a port the harness did not give it"
		}
		if !e.remote.Addr().IsLoopback() {
			return "has a TCP peer off this machine"
		}
		if e.local.Port() == uint16(s.port) || e.remote.Port() == uint16(s.port) || e.remote.Port() == uint16(s.trap) {
			return ""
		}
		return "has a TCP peer on a port the harness did not record"
	case "udp":
		return "opened a UDP socket"
	case "unix":
		for _, path := range []string{e.path, e.peer} {
			if path != "" && !within(path, s.root) {
				return "uses a Unix socket outside its scratch directory"
			}
		}
		return ""
	case "exe":
		if s.executables[e.path] {
			return ""
		}
		for _, dir := range s.executableDirs {
			if within(e.path, dir) {
				return ""
			}
		}
		return "runs a program the harness did not put on its PATH"
	default:
		for _, dir := range s.forbidden {
			if within(e.path, dir) && !within(e.path, s.root) {
				return "opened a file in " + dir
			}
		}
		return ""
	}
}

func within(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// backend reads the open descriptors of a set of processes.
type backend interface {
	endpoints(pids []int) ([]endpoint, error)
}

// watcher polls every running host's process tree for the whole run, so a
// connection opened and closed between two checks is still likely to be seen.
type watcher struct {
	backend  backend
	interval time.Duration
	roots    func() map[int]scope

	mu        sync.Mutex
	out       io.Writer
	inventory *inventory
	found     []string
	seen      map[string]bool
	err       error
	stop      chan struct{}
	done      chan struct{}
}

func newWatcher(b backend, interval time.Duration, roots func() map[int]scope) *watcher {
	return newWatcherTo(os.Stderr, b, interval, roots)
}

func newWatcherTo(out io.Writer, b backend, interval time.Duration, roots func() map[int]scope) *watcher {
	return &watcher{backend: b, interval: interval, roots: roots, out: out, inventory: newInventory(out), seen: map[string]bool{}}
}

func (w *watcher) start() {
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			w.poll()
			select {
			case <-w.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (w *watcher) halt() {
	if w.stop == nil {
		return
	}
	close(w.stop)
	<-w.done
	w.stop = nil
}

// poll checks every process descended from a host's daemon against that
// host's scope.
func (w *watcher) poll() {
	roots := w.roots()
	if len(roots) == 0 {
		return
	}
	owner, err := descendants(roots)
	if err == nil {
		pids := make([]int, 0, len(owner))
		for pid := range owner {
			pids = append(pids, pid)
		}
		var found []endpoint
		if found, err = w.backend.endpoints(pids); err == nil {
			scopes := make([]scope, 0, len(roots))
			for _, s := range roots {
				scopes = append(scopes, s)
			}
			w.mu.Lock()
			w.inventory.sample(scopes)
			w.mu.Unlock()
			for _, e := range found {
				s, ok := owner[e.pid]
				if !ok {
					continue
				}
				w.mu.Lock()
				w.inventory.note(s, e)
				w.mu.Unlock()
				if why := s.check(e); why != "" {
					w.record(fmt.Sprintf("%s %s: %s", s.name, why, e))
				}
			}
		}
	}
	if err != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
	}
}

func (w *watcher) record(violation string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[violation] {
		return
	}
	w.seen[violation] = true
	w.found = append(w.found, violation)
	fmt.Fprintf(w.out, "ios-live-harness: isolation: %s\n", violation)
}

// violations lists everything the watcher saw, and counts a failure to read
// the processes as one: an unchecked run does not pass.
func (w *watcher) violations() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.found...)
	if w.err != nil {
		out = append(out, "the watcher could not read the host processes: "+w.err.Error())
	}
	return out
}

func (w *watcher) blind() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inventory.problems()
}

func (w *watcher) waitForProgram(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		seen := w.inventory.programs[pid]
		w.mu.Unlock()
		if seen {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (w *watcher) missed(problem string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inventory.missing = append(w.inventory.missing, problem)
}

func (w *watcher) summary() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inventory.summary()
}

// descendants maps each host daemon and every process below it to the
// daemon's scope.
func descendants(roots map[int]scope) (map[int]scope, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil {
			children[ppid] = append(children[ppid], pid)
		}
	}
	owner := map[int]scope{}
	for root, s := range roots {
		queue := []int{root}
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			if _, ok := owner[pid]; ok {
				continue
			}
			owner[pid] = s
			queue = append(queue, children[pid]...)
		}
	}
	return owner, nil
}
