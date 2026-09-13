package surfaces

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeDiscoversSocketWithoutBridgeAndExcludesProxies(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join("/tmp/cc-socks", strconv.Itoa(os.Getpid())+".sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("test PID socket already exists")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	start, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"pid": os.Getpid(), "sessionId": "local-id", "name": "native", "cwd": "/fixture", "status": "idle", "procStart": string(start), "messagingSocketPath": socket}
	write := func(record map[string]any) {
		t.Helper()
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(record)
	adapter := NewClaude("", home)
	sessions, err := adapter.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "local-id" || sessions[0].Transport != "uds" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if sessions[0].Transcript != filepath.Join(home, ".claude", "projects", "-fixture", "local-id.jsonl") {
		t.Fatal("transcript escaped adapter home")
	}
	record["bridgeSessionId"] = "session_bridge"
	write(record)
	resolved, err := adapter.Resolve(context.Background(), "local-id")
	if err != nil || resolved.ID != "session_bridge" {
		t.Fatalf("local sender id resolve=%+v err=%v", resolved, err)
	}
	record["agenthail"] = "peer-worker"
	write(record)
	sessions, err = adapter.List(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("proxy rediscovered=%+v err=%v", sessions, err)
	}
	delete(record, "agenthail")
	delete(record, "bridgeSessionId")
	record["procStart"] = "stale process"
	write(record)
	sessions, err = adapter.List(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("stale PID admitted=%+v err=%v", sessions, err)
	}
}

func TestClaudeProcessStartAcceptsUTCRecordForLocalPSOutput(t *testing.T) {
	instant := time.Date(2026, time.September, 13, 3, 2, 13, 0, time.UTC)
	localLocation := time.FixedZone("EDT", -4*60*60)
	local := instant.In(localLocation)
	if !sameClaudeProcessStartInLocations(
		instant.Format(claudeProcessStartLayout), time.UTC,
		local.Format(claudeProcessStartLayout), localLocation,
	) {
		t.Fatal("same process instant in UTC and local time was rejected")
	}
	if sameClaudeProcessStartInLocations(
		instant.Format(claudeProcessStartLayout), time.UTC,
		local.Add(time.Second).Format(claudeProcessStartLayout), localLocation,
	) {
		t.Fatal("different process start instant was accepted")
	}
}

func TestNativeClaudeRejectsControlsBeforeExternalIO(t *testing.T) {
	adapter := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "native", Transport: "uds", Surface: surface.KindClaude}
	ctx := context.Background()
	if _, err := adapter.Send(ctx, session, "/compact"); !surface.IsDeliveryTerminal(err) {
		t.Fatalf("slash error=%v", err)
	}
	if err := adapter.Steer(ctx, session, "hello"); err == nil {
		t.Fatal("native steer accepted")
	}
	if err := adapter.Interrupt(ctx, session); err != surface.ErrUnsupported {
		t.Fatalf("interrupt=%v", err)
	}
	if _, err := adapter.Model(ctx, session, "sonnet"); err != surface.ErrUnsupported {
		t.Fatalf("model=%v", err)
	}
}
