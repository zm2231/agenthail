package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestPeerWorkerEnvelopeReachesBusySteerOutbox(t *testing.T) {
	home := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "ahp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	reg, err := registry.Open(filepath.Join(home, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	target := surface.Session{ID: "target", Surface: surface.KindCodex, Transport: "managed", Status: surface.StatusBusy}
	if err := reg.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{kind: surface.KindCodex, accepted: true, caps: surface.Capabilities{Send: true, Steer: true}, observations: map[string]*surface.TurnObservation{target.ID: {Status: surface.StatusBusy, ActiveTurnID: "turn-1"}}}
	d := New(reg, []surface.Surface{fake})
	parentR, parentW := io.Pipe()
	readyCh := make(chan claudepeer.Ready, 1)
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- claudepeer.RunWorker(context.Background(), claudepeer.Config{Home: home, RegistryPath: reg.Path(), Session: target, SocketDir: socketDir, ControlPath: filepath.Join(socketDir, "control.sock"), ManifestPath: filepath.Join(socketDir, "manifest.json"), BusyDelivery: "steer"}, parentR, readyCapture{readyCh})
	}()
	var ready claudepeer.Ready
	select {
	case ready = <-readyCh:
	case err := <-workerDone:
		t.Fatalf("worker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not become ready")
	}
	conn, err := net.Dial("unix", ready.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	frame := `{"msgV":1,"msg_id":"00000000-0000-4000-8000-000000000001","type":"user","from":"uds:` + ready.SocketPath + `","message":{"role":"user","content":"steer from worker"}}` + "\n"
	if _, err := io.WriteString(conn, frame); err != nil {
		t.Fatal(err)
	}
	_ = conn.(*net.UnixConn).CloseWrite()
	_, _ = io.ReadAll(conn)
	_ = conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for reg.QueueCount(target.ID) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := reg.QueueCount(target.ID); got != 1 {
		t.Fatalf("worker queue count=%d", got)
	}
	d.observeSession(context.Background(), fake, &target)
	if len(fake.steered) != 1 || fake.steered[0] != "[Claude peer message from uds:"+ready.SocketPath+"]\nsteer from worker" {
		t.Fatalf("steered=%q", fake.steered)
	}
	if got := reg.QueueCount(target.ID); got != 0 {
		t.Fatalf("queue count after outbox=%d", got)
	}
	_ = parentW.Close()
	if err := <-workerDone; err != nil {
		t.Fatal(err)
	}
}

type readyCapture struct{ ch chan<- claudepeer.Ready }

func (w readyCapture) Write(data []byte) (int, error) {
	var ready claudepeer.Ready
	if err := json.Unmarshal(data, &ready); err == nil {
		w.ch <- ready
	}
	return len(data), nil
}
