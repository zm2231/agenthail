package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

func debugSocketHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-debug-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return home
}

func debugClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", DebugSocketPath())
	}}}
}

func TestTheDebugSocketServesEachRuntimeProfileToItsOwnerOnly(t *testing.T) {
	home := debugSocketHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	stale, err := net.Listen("unix", DebugSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	stop, err := d.startDebugSocket()
	if err != nil {
		t.Fatalf("a socket left by a stopped daemon blocked startup: %v", err)
	}
	info, err := os.Stat(DebugSocketPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode=%v err=%v", info.Mode().Perm(), err)
	}
	client := debugClient()
	for name, sample := range map[string]string{"heap": "inuse_space", "allocs": "alloc_space", "goroutine": "goroutine"} {
		response, err := client.Get("http://daemon/profiles/" + name)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := profile.Parse(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d err=%v", name, response.StatusCode, err)
		}
		found := false
		for _, kind := range parsed.SampleType {
			found = found || kind.Type == sample
		}
		if !found {
			t.Fatalf("%s profile has no %s samples: %v", name, sample, parsed.SampleType)
		}
	}
	response, err := client.Get("http://daemon/memstats")
	if err != nil {
		t.Fatal(err)
	}
	var stats runtime.MemStats
	err = json.NewDecoder(response.Body).Decode(&stats)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || stats.HeapSys == 0 || stats.Sys < stats.HeapSys || stats.HeapIdle < stats.HeapReleased {
		t.Fatalf("memstats: status=%d err=%v %+v", response.StatusCode, err, stats)
	}
	if response, err := client.Get("http://daemon/profiles/cpu"); err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("an unlisted profile was served: %v %v", response, err)
	}
	stop()
	if _, err := os.Lstat(DebugSocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket left behind after stop: %v", err)
	}
}

func TestTheDebugSocketLeavesAFileAtItsPathAlone(t *testing.T) {
	home := debugSocketHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DebugSocketPath(), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{log: log.New(io.Discard, "", 0)}
	if _, err := d.startDebugSocket(); err == nil {
		t.Fatal("started over a regular file")
	}
	if data, _ := os.ReadFile(DebugSocketPath()); string(data) != "keep" {
		t.Fatalf("the file was replaced: %q", data)
	}
}

func TestADebugClientThatStopsReadingCannotHoldTheServer(t *testing.T) {
	writeTimeout, grace := debugWriteTimeout, debugShutdownGrace
	debugWriteTimeout, debugShutdownGrace = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { debugWriteTimeout, debugShutdownGrace = writeTimeout, grace })
	home := debugSocketHome(t)
	path := filepath.Join(home, "d.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	writeEnded := make(chan error, 1)
	release := make(chan struct{})
	handlerDone := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /flood", func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 64<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				writeEnded <- err
				return
			}
		}
	})
	mux.HandleFunc("GET /stuck", func(w http.ResponseWriter, _ *http.Request) {
		defer close(handlerDone)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
	})
	stop := serveDebug(listener, mux, t.Logf)
	t.Cleanup(func() { close(release) })

	flood, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer flood.Close()
	if _, err := flood.Write([]byte("GET /flood HTTP/1.1\r\nHost: daemon\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writeEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("a response nobody reads kept writing past the write deadline")
	}

	stuck, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()
	if _, err := stuck.Write([]byte("GET /stuck HTTP/1.1\r\nHost: daemon\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := stuck.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited on a stuck response past its grace")
	}
	stuck.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(stuck); err != nil {
		t.Fatalf("the stuck connection was left open after shutdown: %v", err)
	}
}
