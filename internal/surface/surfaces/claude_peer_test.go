package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestClaudeLifecycleCapabilitiesFollowTheRuntime(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	for _, tc := range []struct {
		runtime surface.RuntimeKind
		want    surface.Lifecycle
	}{
		{surface.RuntimeClaudeBackground, surface.Lifecycle{Status: true, Logs: true, Stop: true, Resume: true, Adopt: true}},
		{surface.RuntimeClaudeHost, surface.Lifecycle{Status: true, Logs: true, Stop: true, Resume: true}},
		{surface.RuntimeClaudeCLI, surface.Lifecycle{Adopt: true}},
	} {
		session := &surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: tc.runtime, Transport: "uds", Bridge: "session_bridge", Transcript: "/home/test/.claude/projects/-work/s.jsonl", HasLocal: true}
		if got := adapter.SessionCapabilities(session).Lifecycle; got != tc.want {
			t.Fatalf("%s lifecycle=%+v, want %+v", tc.runtime, got, tc.want)
		}
		adapter.DisableRemoteControl()
		if got := adapter.SessionCapabilities(session).Lifecycle; got != tc.want {
			t.Fatalf("%s without Remote Control lifecycle=%+v, want %+v", tc.runtime, got, tc.want)
		}
		adapter = NewClaude(nil, t.TempDir())
	}
	if adapter.Capabilities().Lifecycle != (surface.Lifecycle{Status: true, Logs: true, Stop: true, Resume: true, Adopt: true}) {
		t.Fatal("the adapter advertises less lifecycle than its sessions reach")
	}
	var codex surface.Surface = NewCodex("")
	if codex.Capabilities().Lifecycle != (surface.Lifecycle{}) {
		t.Fatalf("Codex advertises a lifecycle: %+v", codex.Capabilities().Lifecycle)
	}
}

func TestClaudeDiscoversSocketWithoutBridgeAndExcludesProxies(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := claudetest.ClaimSocket(t, os.Getpid())
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
	transcript := writeClaudeProjectTranscript(t, home, "-fixture", "local-id")
	adapter := NewClaude(nil, home)
	sessions, err := adapter.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "local-id" || sessions[0].Transport != "uds" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if sessions[0].Transcript != transcript {
		t.Fatal("transcript escaped adapter home")
	}
	for kind, want := range map[string]surface.RuntimeKind{"bg": surface.RuntimeClaudeBackground, "interactive": surface.RuntimeClaudeCLI} {
		record["kind"] = kind
		write(record)
		sessions, err = adapter.List(context.Background())
		if err != nil || len(sessions) != 1 || sessions[0].RuntimeKind != want {
			t.Fatalf("record kind %s: sessions=%+v err=%v, want runtime %s", kind, sessions, err, want)
		}
	}
	delete(record, "kind")
	record["bridgeSessionId"] = "session_bridge"
	write(record)
	resolved, err := adapter.Resolve(context.Background(), "local-id")
	if err != nil || resolved.ID != "local-id" || resolved.Bridge != "session_bridge" {
		t.Fatalf("conversation with a Remote Control attachment=%+v err=%v", resolved, err)
	}
	for _, role := range []string{"peer-worker", "peer-relay"} {
		record["agenthail"] = role
		write(record)
		sessions, err = adapter.List(context.Background())
		if err != nil || len(sessions) != 0 {
			t.Fatalf("%s rediscovered=%+v err=%v", role, sessions, err)
		}
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

func TestProcessesSharingAConversationAreHoldersAndAPIDTargetsOne(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("/tmp/cc-socks", 0700); err != nil {
		t.Fatal(err)
	}
	first := exec.Command("/bin/sh", "-c", "sleep 10")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = first.Process.Kill()
		_ = first.Wait()
	}()
	write := func(pid int, startedAt int64) string {
		command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
		command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
		start, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		socket := claudetest.ClaimSocket(t, pid)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		record := map[string]any{"pid": pid, "sessionId": "shared", "startedAt": startedAt, "name": "shared", "cwd": "/fixture", "status": "idle", "version": "2.1.270", "procStart": string(start), "messagingSocketPath": socket}
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(sessionsDir, strconv.Itoa(pid)+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return socket
	}
	firstPID, laterPID := first.Process.Pid, os.Getpid()
	write(firstPID, 100)
	write(laterPID, 200)

	adapter := NewClaude(nil, home)
	sessions, err := adapter.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].PID != 0 || len(sessions[0].Holders) != 2 || sessions[0].Holders[0].PID != firstPID || sessions[0].Holders[1].PID != laterPID {
		t.Fatalf("listed=%+v err=%v", sessions, err)
	}
	if _, _, found, err := adapter.nativeCaller(context.Background(), "shared"); err != nil || found {
		t.Fatalf("a shared session id picked one process's socket: found=%v err=%v", found, err)
	}
	resolved, err := adapter.Resolve(context.Background(), strconv.Itoa(laterPID))
	if err != nil || resolved.PID != laterPID || resolved.HolderPID != laterPID || len(resolved.Holders) != 0 {
		t.Fatalf("pid target=%+v err=%v", resolved, err)
	}
}

func TestNativeClaudeSenderUsesItsOwnSocket(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("/bin/sh", "-c", "sleep 5")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	}()
	if err := os.MkdirAll("/tmp/cc-socks", 0700); err != nil {
		t.Fatal(err)
	}
	sourceSocket := claudetest.ClaimSocket(t, os.Getpid())
	targetSocket := claudetest.ClaimSocket(t, process.Process.Pid)
	sourceListener, err := net.Listen("unix", sourceSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceListener.Close()
	targetListener, err := net.Listen("unix", targetSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	start := func(pid int) string {
		command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
		command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
		output, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(output)
	}
	write := func(pid int, id, socket string) {
		t.Helper()
		record := map[string]any{"pid": pid, "sessionId": id, "name": id, "cwd": "/fixture", "status": "idle", "version": "2.1.270", "procStart": start(pid), "messagingSocketPath": socket}
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(sessionsDir, strconv.Itoa(pid)+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(os.Getpid(), "source-native", sourceSocket)
	write(process.Process.Pid, "target-native", targetSocket)
	received := make(chan struct {
		From    string          `json:"from"`
		Message json.RawMessage `json:"message"`
	}, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var frame struct {
			From    string          `json:"from"`
			Message json.RawMessage `json:"message"`
		}
		if json.NewDecoder(conn).Decode(&frame) == nil {
			received <- frame
		}
	}()
	adapter := NewClaude(nil, home)
	result, err := adapter.Send(surface.WithReplyTarget(surface.WithSourceSessionID(context.Background(), "source-native"), "@reviewer"), &surface.Session{ID: "target-native", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.Process.Pid, Transport: "uds"}, "status?")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	select {
	case frame := <-received:
		if frame.From != "uds:"+sourceSocket {
			t.Fatalf("from=%q", frame.From)
		}
		if !strings.Contains(string(frame.Message), "status?") || !strings.Contains(string(frame.Message), `from-name=\"agenthail/claude: @reviewer\"`) {
			t.Fatalf("message=%s", frame.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("native sender frame was not delivered")
	}
}

func TestClaudeProcessStartMatchesOnlyTheSameInstant(t *testing.T) {
	instant := time.Date(2026, time.September, 13, 3, 2, 13, 0, time.UTC)
	localLocation := time.FixedZone("EDT", -4*60*60)
	utc := instant.Format(claudeProcessStartLayout)
	local := instant.In(localLocation).Format(claudeProcessStartLayout)
	later := instant.Add(time.Second).Format(claudeProcessStartLayout)
	for _, test := range []struct {
		name, version, recorded, observed string
		same                              bool
	}{
		{name: "UTC record", version: "2.1.270", recorded: utc, observed: utc, same: true},
		{name: "legacy local record", version: "2.1.251", recorded: local, observed: utc, same: true},
		{name: "different instant", version: "2.1.270", recorded: utc, observed: later},
		{name: "legacy record with matching UTC text from a different instant", version: "2.1.251", recorded: utc, observed: utc},
		{name: "unknown record format", version: "unrecognized", recorded: utc, observed: utc},
	} {
		if got := sameClaudeProcessStartInLocation(test.version, test.recorded, test.observed, localLocation); got != test.same {
			t.Fatalf("%s: same=%v want %v", test.name, got, test.same)
		}
	}
}

func TestNativeClaudeRejectsControlsBeforeExternalIO(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
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
	if _, err := adapter.Model(ctx, session, "sonnet"); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("model=%v", err)
	}
}

func TestClaudeDiscoversLinuxSocketUnderXDGRuntimeDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Claude Code binds peer sockets under XDG_RUNTIME_DIR only on Linux")
	}
	runtimeDir, err := os.MkdirTemp("/tmp", "ah-xdg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(runtimeDir, "cc-socks", strconv.Itoa(os.Getpid())+".sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	record := map[string]any{"pid": os.Getpid(), "sessionId": "linux-id", "status": "idle", "version": "2.1.302", "messagingSocketPath": socket}
	if runtime.GOOS == "linux" {
		start, err := procstart.Ticks(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		record["procStart"] = start
	}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := NewClaude(nil, home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "linux-id" || sessions[0].Transport != "uds" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	record["procStart"] = "1"
	data, _ = json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if sessions, err := NewClaude(nil, home).List(context.Background()); err != nil || len(sessions) != 0 {
		t.Fatalf("reused PID with a different start was trusted: sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeWithoutRemoteControlIgnoresBridgeIDs(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := claudetest.ClaimSocket(t, os.Getpid())
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
	record := map[string]any{"pid": os.Getpid(), "sessionId": "local-id", "bridgeSessionId": "session_bridge", "cwd": "/fixture", "status": "idle", "version": "2.1.270", "procStart": string(start), "messagingSocketPath": socket}
	write := func() {
		t.Helper()
		data, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	adapter := NewClaude(nil, home)
	adapter.DisableRemoteControl()
	sessions, err := adapter.List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "local-id" || sessions[0].Transport != "uds" {
		t.Fatalf("mixed record sessions=%+v err=%v", sessions, err)
	}
	if adapter.Route(&sessions[0], surface.AuthorPerson) != surface.RoutePeer {
		t.Fatal("person-authored send routed to Remote Control")
	}
	if _, err := adapter.postMessage(context.Background(), &surface.Session{ID: "local-id", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, "hello"); !surface.IsDeliveryUnavailable(err) {
		t.Fatalf("remote control post err=%v", err)
	}
	record["messagingSocketPath"] = filepath.Join(home, "missing.sock")
	write()
	listener.Close()
	sessions, err = adapter.List(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("bridge-only record listed=%+v err=%v", sessions, err)
	}
}

func TestClaudeRouteDecidesByAuthorBridgeAndSocket(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	live := os.Getpid()
	bridged := &surface.Session{ID: "local-id", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: live}
	socketOnly := &surface.Session{ID: "local-id", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: live}
	exited := &surface.Session{ID: "local-id", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"}
	bridgeOnly := &surface.Session{ID: "local-id", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: live}
	for _, tc := range []struct {
		name    string
		session *surface.Session
		author  surface.Author
		want    surface.Route
	}{
		{"person with bridge", bridged, surface.AuthorPerson, surface.RouteHumanTurn},
		{"unattributed with bridge and socket", bridged, "", surface.RoutePeer},
		{"person with socket only", socketOnly, surface.AuthorPerson, surface.RoutePeer},
		{"person to an exited session", exited, surface.AuthorPerson, surface.RouteHumanTurn},
		{"unattributed to an exited session", exited, "", surface.RoutePeer},
		{"unattributed with bridge only", bridgeOnly, "", surface.RouteSend},
	} {
		if got := adapter.Route(tc.session, tc.author); got != tc.want {
			t.Fatalf("%s: route=%v want %v", tc.name, got, tc.want)
		}
	}
	adapter.DisableRemoteControl()
	if got := adapter.Route(bridged, surface.AuthorPerson); got != surface.RoutePeer {
		t.Fatalf("local-only install routed a person's message to Remote Control: %v", got)
	}
	if got := adapter.Route(exited, surface.AuthorPerson); got != surface.RouteHumanTurn {
		t.Fatalf("local-only install did not resume an exited session for a person's message: %v", got)
	}
}

func TestClaudeSocketSessionStreamsOnlyFromALocalTranscript(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	if got := surface.EffectiveCapabilities(adapter, &surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Transcript: "/tmp/session.jsonl", HasLocal: true}); !got.Stream {
		t.Fatal("reliable local Claude transcript should remain streamable")
	}
	if got := surface.EffectiveCapabilities(adapter, &surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", Transcript: "/tmp/session.jsonl"}); got.Stream {
		t.Fatal("Claude UDS session without a local transcript should not claim stream support")
	}
}

func writeLivePeerRecord(t *testing.T, sessionsDir string, pid int, id, cwd string) {
	t.Helper()
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	start, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join("/tmp/cc-socks", strconv.Itoa(pid)+".sock")
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("test socket already exists %s: %v", socket, err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	record := map[string]any{"pid": pid, "sessionId": id, "name": id, "cwd": cwd, "status": "idle", "version": "2.1.270", "procStart": string(start), "messagingSocketPath": socket}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(sessionsDir, strconv.Itoa(pid)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeResolveReadsTheStateOfOnlyTheSessionItReturns(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	projects := filepath.Join(home, ".claude", "projects", "-fixture")
	for _, dir := range []string{sessionsDir, projects, "/tmp/cc-socks"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	worker := exec.Command("/bin/sh", "-c", "sleep 10")
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = worker.Process.Kill()
		_ = worker.Wait()
	}()
	transcript := func(id string) string {
		path := filepath.Join(projects, id+".jsonl")
		line := `{"type":"user","uuid":"u-` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const target, other = "7d0c3a4e-0000-4000-8000-00000000a001", "7d0c3a4e-0000-4000-8000-00000000a002"
	targetPath, otherPath := transcript(target), transcript(other)
	writeLivePeerRecord(t, sessionsDir, os.Getpid(), target, "/fixture")
	writeLivePeerRecord(t, sessionsDir, worker.Process.Pid, other, "/fixture")
	adapter := NewClaude(nil, home)
	resolved, err := adapter.Resolve(context.Background(), target)
	if err != nil || resolved.ID != target || resolved.Transcript != targetPath || resolved.Status == "" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if _, read := adapter.taskScans[targetPath]; !read {
		t.Fatal("the resolved session's background tasks were not read")
	}
	if _, read := adapter.taskScans[otherPath]; read {
		t.Fatal("resolving one session read another's transcript")
	}
	listed, err := adapter.List(context.Background())
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if _, read := adapter.taskScans[otherPath]; !read {
		t.Fatal("a listing reads every session's state")
	}
}

func TestClaudeResolveExactIDBeatsCwdContainingIt(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("/tmp/cc-socks", 0700); err != nil {
		t.Fatal(err)
	}
	worker := exec.Command("/bin/sh", "-c", "sleep 10")
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = worker.Process.Kill()
		_ = worker.Wait()
	}()
	write := func(pid int, id, cwd string) { writeLivePeerRecord(t, sessionsDir, pid, id, cwd) }
	const coordinator = "4c1d2f8e-0000-4000-8000-00000000c011"
	write(os.Getpid(), coordinator, "/fixture")
	write(worker.Process.Pid, "worker-id", "/scratch/"+coordinator+"/check")

	adapter := NewClaude(nil, home)
	resolved, err := adapter.Resolve(context.Background(), coordinator)
	if err != nil || resolved.ID != coordinator {
		t.Fatalf("exact id target=%+v err=%v", resolved, err)
	}
	resolved, err = adapter.Resolve(context.Background(), "scratch")
	if err != nil || resolved.ID != "worker-id" {
		t.Fatalf("cwd text target=%+v err=%v", resolved, err)
	}
}

func TestClaudeAdvertisesRemoteControlCommandsOnlyWhereControlReachesThem(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	bridged := &surface.Session{ID: "bridged", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"}
	socketOnly := &surface.Session{ID: "socket", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"}
	hosted := &surface.Session{ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds"}
	for _, tc := range []struct {
		name     string
		session  *surface.Session
		commands bool
		model    bool
	}{
		{"bridged", bridged, true, true},
		{"socket only", socketOnly, false, false},
		{"hosted", hosted, false, true},
	} {
		caps := adapter.SessionCapabilities(tc.session)
		if caps.Commands != tc.commands || caps.Model != tc.model {
			t.Fatalf("%s: commands=%v model=%v, want %v %v", tc.name, caps.Commands, caps.Model, tc.commands, tc.model)
		}
	}
	adapter.DisableRemoteControl()
	if caps := adapter.SessionCapabilities(bridged); caps.Commands || caps.Model || caps.Steer || caps.Compact || caps.Interrupt {
		t.Fatalf("an install without Remote Control advertises it: %+v", caps)
	}
	if caps := adapter.SessionCapabilities(hosted); !caps.Model || !caps.Steer || !caps.Interrupt || caps.Commands {
		t.Fatalf("an install without Remote Control dropped the host's controls: %+v", caps)
	}
}

func TestClaudeTellsThePersonWhenTheyReachALiveTerminalOnlyAsANote(t *testing.T) {
	adapter := NewClaude(nil, t.TempDir())
	live := os.Getpid()
	terminal := &surface.Session{ID: "terminal", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: live}
	for name, session := range map[string]*surface.Session{
		"remote control": {ID: "bridged", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: live},
		"hosted":         {ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: live},
		"exited":         {ID: "exited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds"},
	} {
		if notice := adapter.SessionCapabilities(session).TurnNotice; notice != "" {
			t.Fatalf("%s: a session that takes the person's turn has a notice: %q", name, notice)
		}
	}
	if notice := adapter.SessionCapabilities(terminal).TurnNotice; !strings.Contains(notice, "/remote-control") {
		t.Fatalf("live terminal notice = %q", notice)
	}
	adapter.DisableRemoteControl()
	if notice := adapter.SessionCapabilities(terminal).TurnNotice; notice == "" || strings.Contains(notice, "/remote-control") {
		t.Fatalf("an install without Remote Control offers it: %q", notice)
	}
}
