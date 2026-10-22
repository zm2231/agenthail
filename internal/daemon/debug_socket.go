package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

var DebugProfiles = []string{"heap", "allocs", "goroutine"}

func DebugSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "debug.sock")
}

// startDebugSocket serves the runtime profiles on a Unix socket only the
// daemon's user can open. It runs under the daemon lock, so a socket left at
// the path belongs to a daemon that is gone.
func (d *Daemon) startDebugSocket() (func(), error) {
	path := DebugSocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("debug socket path is not a socket: %s", path)
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
	server := &http.Server{Handler: debugProfileHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.Printf("debug socket: %s", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = os.Remove(path)
	}, nil
}

func debugProfileHandler() http.Handler {
	mux := http.NewServeMux()
	for _, name := range DebugProfiles {
		mux.HandleFunc("GET /profiles/"+name, func(w http.ResponseWriter, _ *http.Request) {
			if name == "heap" {
				runtime.GC()
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			if err := pprof.Lookup(name).WriteTo(w, 0); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
	}
	return mux
}
