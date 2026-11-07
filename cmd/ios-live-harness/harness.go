package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type harness struct {
	hosts      []*host
	controlURL string
	server     *http.Server
	watch      *watcher
	scratch    []string
	trap       net.Listener

	forbidden      []string
	executables    map[string]bool
	executableDirs []string
	probes         probeSet

	// closing refuses control commands once shutdown begins; ops counts the
	// ones still running, which shutdown waits for before stopping hosts.
	mu      sync.Mutex
	closing bool
	ops     sync.WaitGroup
}

func newHarness(binary string, count int) (*harness, error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("refusing to run as root")
	}
	if _, err := os.Stat(binary); err != nil {
		return nil, err
	}
	base, err := scratchBase()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	// The hosts' Codex debugging port and push relay point at the trap: a
	// port the harness holds for the whole run, closing every connection,
	// so no other local service can take it.
	trap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go refuseAll(trap)
	h := &harness{trap: trap}
	tools, executables, executableDirs, err := resolveTools(binary)
	if err != nil {
		trap.Close()
		return nil, err
	}
	trapPort := trap.Addr().(*net.TCPAddr).Port
	for i := 0; i < count; i++ {
		host, err := newHost(binary, base, i, trapPort)
		if err != nil {
			h.cleanup()
			return nil, err
		}
		h.hosts = append(h.hosts, host)
		h.scratch = append(h.scratch, host.root)
		if err := host.linkTools(tools); err != nil {
			h.cleanup()
			return nil, err
		}
		if err := host.seed(); err != nil {
			h.cleanup()
			return nil, fmt.Errorf("seed %s: %w", host.name, err)
		}
	}
	h.forbidden = []string{home, "/tmp/cc-socks", "/private/tmp/cc-socks"}
	h.executables, h.executableDirs = executables, executableDirs
	h.watch = newWatcher(newBackend(), 250*time.Millisecond, func() map[int]scope {
		roots := h.probes.roots()
		for _, host := range h.hosts {
			if pid := host.pid(); pid != 0 {
				roots[pid] = h.scope(host)
			}
		}
		return roots
	})
	return h, nil
}

func (h *harness) scope(host *host) scope {
	return scope{name: host.name, root: host.root, port: host.port, trap: host.trap, forbidden: h.forbidden, executables: h.executables, executableDirs: h.executableDirs}
}

func (h *harness) start(ctx context.Context, control string) error {
	listener, err := net.Listen("tcp", control)
	if err != nil {
		return fmt.Errorf("control port: %w", err)
	}
	if addr := listener.Addr().(*net.TCPAddr); !addr.IP.IsLoopback() {
		listener.Close()
		return fmt.Errorf("control port %s is not on loopback", addr)
	}
	h.controlURL = "http://" + listener.Addr().String()
	h.server = &http.Server{Handler: h.controlHandler(ctx), ReadHeaderTimeout: 10 * time.Second}
	go h.server.Serve(listener)

	h.watch.start()
	for _, host := range h.hosts {
		if err := host.start(ctx); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "ios-live-harness: %s pid %d on %s, scratch %s\n", host.name, host.pid(), host.endpoint(), host.root)
	}
	if err := h.probeTools(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ios-live-harness: control %s\n", h.controlURL)
	return nil
}

// shutdown closes the control port, waits for control commands still
// running, then stops every host while the watcher takes a last look.
func (h *harness) shutdown() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	if h.server != nil {
		h.server.Close()
	}
	h.ops.Wait()
	for _, host := range h.hosts {
		host.stop()
		host.endTurn()
	}
	if h.watch != nil {
		h.watch.halt()
	}
}

// cleanup deletes the scratch directories the harness created, by the paths
// it recorded.
func (h *harness) cleanup() {
	for _, host := range h.hosts {
		host.stop()
		host.endTurn()
	}
	if h.trap != nil {
		h.trap.Close()
	}
	for _, dir := range h.scratch {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "ios-live-harness: remove %s: %v\n", dir, err)
		}
	}
}

type hostStatus struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Running  bool   `json:"running"`
}

// controlHandler serves the commands the UI tests send. It never runs a
// shell command and touches nothing outside the hosts' scratch directories.
//
//	GET  /hosts             every host and whether it is running
//	POST /hosts/{i}/link    a new pairing link for host i
//	POST /hosts/{i}/reply   append an assistant reply to one of host i's seeded conversations
//	POST /hosts/{i}/turn/start   open a turn in a running Claude conversation on host i
//	POST /hosts/{i}/turn/finish  end that turn as Claude does
//	POST /hosts/{i}/stop    stop host i's daemon
//	POST /hosts/{i}/start   start host i's daemon again on the same port
func (h *harness) controlHandler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	status := func(host *host) hostStatus {
		return hostStatus{Index: host.index, Name: host.name, Endpoint: host.endpoint(), Running: host.running()}
	}
	mux.HandleFunc("GET /hosts", func(w http.ResponseWriter, r *http.Request) {
		out := make([]hostStatus, 0, len(h.hosts))
		for _, host := range h.hosts {
			out = append(out, status(host))
		}
		writeJSON(w, http.StatusOK, out)
	})
	withHost := func(run func(http.ResponseWriter, *host)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			i, err := strconv.Atoi(r.PathValue("i"))
			if err != nil || i < 0 || i >= len(h.hosts) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such host"})
				return
			}
			h.mu.Lock()
			if h.closing {
				h.mu.Unlock()
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the harness is shutting down"})
				return
			}
			h.ops.Add(1)
			h.mu.Unlock()
			defer h.ops.Done()
			run(w, h.hosts[i])
		}
	}
	mux.HandleFunc("POST /hosts/{i}/link", withHost(func(w http.ResponseWriter, host *host) {
		link, err := host.pairingLink()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": link})
	}))
	mux.HandleFunc("POST /hosts/{i}/reply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Conversation int    `json:"conversation"`
			Text         string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send {\"conversation\": n, \"text\": \"...\"}"})
			return
		}
		withHost(func(w http.ResponseWriter, host *host) {
			id, err := host.appendReply(body.Conversation, body.Text)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"session": id})
		})(w, r)
	})
	turn := func(run func(*host, string) (string, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.Text == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send {\"text\": \"...\"}"})
				return
			}
			withHost(func(w http.ResponseWriter, host *host) {
				id, err := run(host, body.Text)
				if err != nil {
					writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"session": id})
			})(w, r)
		}
	}
	mux.HandleFunc("POST /hosts/{i}/turn/start", turn((*host).startTurn))
	mux.HandleFunc("POST /hosts/{i}/turn/finish", turn((*host).finishTurn))
	mux.HandleFunc("POST /hosts/{i}/stop", withHost(func(w http.ResponseWriter, host *host) {
		host.stop()
		writeJSON(w, http.StatusOK, status(host))
	}))
	mux.HandleFunc("POST /hosts/{i}/start", withHost(func(w http.ResponseWriter, host *host) {
		if err := host.start(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status(host))
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}

func refuseAll(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}
}
