package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeDaemon stands in for `agenthail daemon-run`: it serves the dashboard
// port its HOME's dashboard.json names until it is stopped.
func fakeDaemon() {
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".agenthail", "dashboard.json"))
	must(err)
	var config struct{ Listen string }
	must(json.Unmarshal(data, &config))
	listener, err := net.Listen("tcp", config.Listen)
	must(err)
	must(http.Serve(listener, http.NotFoundHandler()))
}

func newTestHarness(t *testing.T) *harness {
	t.Helper()
	h, err := newHarness(os.Args[0], 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.cleanup)
	return h
}

func TestNoControlCommandStartsAHostOnceShutdownBegins(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	if err := h.start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	h.shutdown()

	response := httptest.NewRecorder()
	h.controlHandler(ctx).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/hosts/0/start", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("start after shutdown answered %d: %s", response.Code, response.Body)
	}
	if h.hosts[0].running() {
		h.hosts[0].stop()
		t.Fatal("a host started after shutdown, where neither the watcher nor cleanup would see it")
	}
}

func TestTheTrapPortStaysHeldByTheHarnessForTheWholeRun(t *testing.T) {
	h := newTestHarness(t)
	address := h.trap.Addr().String()
	if listener, err := net.Listen("tcp", address); err == nil {
		listener.Close()
		t.Fatalf("another process could take the hosts' Codex and push relay port %s", address)
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if n, err := conn.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("the trap port answered: %d bytes, %v", n, err)
	}
}
