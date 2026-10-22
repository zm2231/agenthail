package daemon

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

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
