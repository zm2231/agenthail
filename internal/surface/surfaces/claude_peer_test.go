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
	t.Setenv("TZ", "America/New_York")
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
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid()))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	start, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"pid": os.Getpid(), "sessionId": "local-id", "name": "native", "cwd": "/fixture", "status": "idle", "version": "2.1.270", "procStart": string(start), "messagingSocketPath": socket}
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

func TestClaudeProcessStartAcceptsUTCAndLegacyLocalRecords(t *testing.T) {
	instant := time.Date(2026, time.September, 13, 3, 2, 13, 0, time.UTC)
	localLocation := time.FixedZone("EDT", -4*60*60)
	local := instant.In(localLocation)
	if !sameClaudeProcessStartInLocation(
		"2.1.270", instant.Format(claudeProcessStartLayout),
		instant.Format(claudeProcessStartLayout), localLocation,
	) {
		t.Fatal("UTC record was rejected")
	}
	if !sameClaudeProcessStartInLocation(
		"2.1.251", local.Format(claudeProcessStartLayout),
		instant.Format(claudeProcessStartLayout), localLocation,
	) {
		t.Fatal("legacy local record was rejected")
	}
	if sameClaudeProcessStartInLocation(
		"2.1.270", instant.Format(claudeProcessStartLayout),
		instant.Add(time.Second).Format(claudeProcessStartLayout), localLocation,
	) {
		t.Fatal("different process start instant was accepted")
	}
}

func TestClaudeProcessStartRejectsMatchingTextFromDifferentInstants(t *testing.T) {
	instant := time.Date(2026, time.September, 13, 3, 2, 13, 0, time.UTC)
	localLocation := time.FixedZone("EDT", -4*60*60)
	text := instant.Format(claudeProcessStartLayout)
	if sameClaudeProcessStartInLocation("2.1.251", text, text, localLocation) {
		t.Fatal("matching wall-clock text admitted a recycled PID with a different start instant")
	}
	if !sameClaudeProcessStartInLocation("2.1.270", text, text, localLocation) {
		t.Fatal("UTC process start was rejected")
	}
	if sameClaudeProcessStartInLocation("unrecognized", text, text, localLocation) {
		t.Fatal("record with unknown timestamp format was admitted")
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
