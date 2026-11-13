package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"
)

var DebugProfiles = []string{"heap", "allocs", "goroutine"}

const (
	DefaultCPUProfileSeconds = 30
	MaxCPUProfileSeconds     = 300
)

func DebugSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "debug.sock")
}

// startDebugSocket serves the runtime profiles on a Unix socket only the
// daemon's user can open.
func (d *Daemon) startDebugSocket() (func(), error) {
	path := DebugSocketPath()
	listener, err := listenUserSocket(path)
	if err != nil {
		return nil, err
	}
	stop := serveDebug(listener, debugProfileHandler(), d.log.Printf)
	return func() {
		stop()
		_ = os.Remove(path)
	}, nil
}

// listenUserSocket listens at path for the daemon's user alone. It runs under
// the daemon lock, so a socket left at path belongs to a daemon that is gone.
func listenUserSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("socket path is not a socket: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

var (
	debugWriteTimeout  = time.Minute
	debugShutdownGrace = 5 * time.Second
)

// serveDebug bounds every response by a write deadline, so a client that stops
// reading cannot hold a goroutine, and closes whatever is still open once the
// shutdown grace runs out.
func serveDebug(listener net.Listener, handler http.Handler, logf func(string, ...any)) func() {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: debugWriteTimeout, IdleTimeout: debugWriteTimeout}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("debug socket: %s", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), debugShutdownGrace)
		defer cancel()
		if err := server.Shutdown(ctx); errors.Is(err, context.DeadlineExceeded) {
			_ = server.Close()
		}
	}
}

func debugProfileHandler() http.Handler {
	mux := http.NewServeMux()
	// The runtime's own accounting, read as is: how much of the heap is live,
	// idle, or returned to the system.
	mux.HandleFunc("GET /memstats", func(w http.ResponseWriter, _ *http.Request) {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
	})
	for _, name := range DebugProfiles {
		mux.HandleFunc("GET /profiles/"+name, func(w http.ResponseWriter, _ *http.Request) {
			if name == "heap" {
				runtime.GC()
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			if err := pprof.Lookup(name).WriteTo(w, 0); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			}
		})
	}
	mux.HandleFunc("GET /profiles/cpu", serveCPUProfile)
	return mux
}

// serveCPUProfile samples the daemon for the seconds asked, stopping early
// when the client goes away. The write deadline is moved past the sampling
// so the profile can still be sent; only one runs at a time.
func serveCPUProfile(w http.ResponseWriter, r *http.Request) {
	seconds := DefaultCPUProfileSeconds
	if raw := r.URL.Query().Get("seconds"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > MaxCPUProfileSeconds {
			writeAPIError(w, http.StatusBadRequest, "invalid_seconds", fmt.Sprintf("seconds must be between 1 and %d", MaxCPUProfileSeconds))
			return
		}
		seconds = parsed
	}
	duration := time.Duration(seconds) * time.Second
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(duration + debugWriteTimeout)); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	var profile bytes.Buffer
	if err := pprof.StartCPUProfile(&profile); err != nil {
		writeAPIError(w, http.StatusConflict, "profile_running", err.Error())
		return
	}
	timer := time.NewTimer(duration)
	select {
	case <-timer.C:
	case <-r.Context().Done():
		timer.Stop()
		pprof.StopCPUProfile()
		return
	}
	pprof.StopCPUProfile()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(profile.Bytes())
}
