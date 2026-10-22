package cli

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

func serveFakeDebugSocket(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-debug-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Dir(daemon.DebugSocketPath()), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", daemon.DebugSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
}

func TestDebugHeapWritesEachProfileTheDaemonServes(t *testing.T) {
	serveFakeDebugSocket(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("profile:" + strings.TrimPrefix(r.URL.Path, "/profiles/")))
	})
	dir := filepath.Join(t.TempDir(), "profiles")
	app := &App{catalogDaemonRunning: func() bool { return true }}
	output, err := captureStdout(t, func() error { return app.Run([]string{"debug", "heap", dir}) })
	if err != nil {
		t.Fatal(err)
	}
	paths := strings.Fields(output)
	if len(paths) != len(daemon.DebugProfiles) {
		t.Fatalf("output=%q", output)
	}
	for index, name := range daemon.DebugProfiles {
		path := paths[index]
		if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), name+"-") || !strings.HasSuffix(path, ".pb.gz") {
			t.Fatalf("%s written to %s", name, path)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "profile:"+name {
			t.Fatalf("%s: %q %v", name, data, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", name, info.Mode().Perm())
		}
	}
}

func TestDebugHeapReportsADaemonError(t *testing.T) {
	serveFakeDebugSocket(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no profile", http.StatusInternalServerError)
	})
	dir := t.TempDir()
	app := &App{catalogDaemonRunning: func() bool { return true }}
	if err := app.Run([]string{"debug", "heap", dir}); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("err=%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a failed profile left files: %v", entries)
	}
}

func TestDebugHeapNeedsARunningDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{catalogDaemonRunning: func() bool { return false }}
	if err := app.Run([]string{"debug", "heap", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err=%v", err)
	}
}

type failingClose struct{ *os.File }

func (f failingClose) Close() error {
	f.File.Close()
	return errors.New("flush failed")
}

func TestDebugHeapRemovesAProfileThatFailsToClose(t *testing.T) {
	serveFakeDebugSocket(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("profile")) })
	create := createProfileFile
	createProfileFile = func(path string) (io.WriteCloser, error) {
		file, err := create(path)
		if err != nil {
			return nil, err
		}
		return failingClose{file.(*os.File)}, nil
	}
	t.Cleanup(func() { createProfileFile = create })
	dir := t.TempDir()
	app := &App{catalogDaemonRunning: func() bool { return true }}
	if err := app.Run([]string{"debug", "heap", dir}); err == nil || !strings.Contains(err.Error(), "flush failed") {
		t.Fatalf("err=%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a profile that failed to close was left: %v", entries)
	}
}
