package peerbridge

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestManagerOwnsDistinctPeersAndRecoversChildExit(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	binary := filepath.Join(home, "agenthail")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/agenthail")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	for _, id := range []string{"recent", "older"} {
		if err := reg.RegisterSession(surface.Session{ID: id, Surface: surface.KindNotion, Name: id, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager, err := Start(ctx, home, reg, binary)
	if err != nil {
		t.Fatal(err)
	}
	manager.socketDir = filepath.Join(home, "socks")
	defer manager.Close()
	if err := manager.Ensure(ctx, "recent"); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(ctx, home, "older"); err != nil {
		t.Fatal(err)
	}
	first, second := manager.children["recent"], manager.children["older"]
	if first.process.Pid == second.process.Pid {
		t.Fatal("agents share a PID")
	}
	if err := Ensure(ctx, home, "older"); err != nil {
		t.Fatal(err)
	}
	if manager.children["older"] != second {
		t.Fatal("duplicate registration spawned a new worker")
	}
	socket := filepath.Join(manager.socketDir, strconv.Itoa(second.process.Pid)+".sock")
	messageID := uuid.NewString()
	for range 2 {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		err = json.NewEncoder(conn).Encode(map[string]any{"msgV": 1, "msg_id": messageID, "type": "user", "from": "uds:" + filepath.Join(manager.socketDir, strconv.Itoa(first.process.Pid)+".sock"), "message": map[string]string{"role": "user", "content": "peer reply"}})
		if err != nil {
			t.Fatal(err)
		}
		conn.(*net.UnixConn).CloseWrite()
		if _, err := io.ReadAll(conn); err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	if count := reg.QueueCount("older"); count != 1 {
		t.Fatalf("duplicate inbound queue count=%d", count)
	}
	if err := reg.ReplaceAlias("renamed", "older"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond)
	data, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(second.process.Pid)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Name != "agenthail/notion: renamed" {
		t.Fatalf("heartbeat metadata=%s", data)
	}
	if err := second.process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-second.done
	if err := Ensure(ctx, home, "older"); err != nil {
		t.Fatalf("worker restart: %v", err)
	}
	if manager.children["older"].process.Pid == second.process.Pid {
		t.Fatal("dead worker reused")
	}
	children := manager.children
	manager.Close()
	for _, entry := range children {
		select {
		case <-entry.done:
		default:
			t.Fatal("child remained alive")
		}
	}
	files, _ := filepath.Glob(filepath.Join(home, ".agenthail", "peers", "*.sock"))
	if len(files) != 0 {
		t.Fatalf("control sockets remain: %v", files)
	}
	files, _ = filepath.Glob(filepath.Join(manager.socketDir, "*.sock"))
	if len(files) != 0 {
		t.Fatalf("native sockets remain: %v", files)
	}
	files, _ = filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	if len(files) != 0 {
		t.Fatalf("peer records remain: %v", files)
	}
}

func TestManagerRejectsMissingSourceAndPreservesExistingFile(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := os.WriteFile(managerPath(home), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), home, reg, "missing"); err == nil {
		t.Fatal("existing file replaced")
	}
	data, _ := os.ReadFile(managerPath(home))
	if string(data) != "keep" {
		t.Fatal("existing file changed")
	}
}
