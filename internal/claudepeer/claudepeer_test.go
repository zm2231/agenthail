package claudepeer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestWorkerRegistersQueuesAndCleansUpOnParentEOF(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	home, regPath := shortTempDir(t, "cp-home-"), filepath.Join(t.TempDir(), "registry.db")
	socketDir := shortTempDir(t, "cp-socks-")
	s := surface.Session{ID: "operator", Surface: surface.KindClaude, Name: "peer", Cwd: "/tmp/project", Status: surface.StatusIdle}
	reg, err := registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	writeDashboardPolicy(t, home, "steer")
	r, stop := startWorker(t, Config{Home: home, RegistryPath: regPath, Session: s, SocketDir: socketDir})
	var record sessionRecord
	data, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(r.PID)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(data, &record) != nil || record.Agenthail == "" || record.PeerProtocol != 1 || record.MessagingSocketPath != r.SocketPath {
		t.Fatalf("bad record: %s", data)
	}
	if want := claudeProcStart(t, r.PID); record.ProcStart != want {
		t.Fatalf("record procStart=%q, want Claude's UTC identity %q", record.ProcStart, want)
	}
	sendFrames(t, r.SocketPath, frame{MsgV: 1, MsgID: uuid.New().String(), Type: "user", From: "uds:" + r.SocketPath, Message: mustJSON(messageBody{Role: "user", Content: "<cross-session-message from-session=\"source\">\nhello\n</cross-session-message>"})})
	if got := reg.QueueCount(s.ID); got != 1 {
		t.Fatalf("queue count=%d", got)
	}
	item, err := reg.QueueItem(1)
	if err != nil || item.BusyDelivery != "steer" {
		t.Fatalf("worker queue mode=%q err=%v", item.BusyDelivery, err)
	}
	history, err := reg.ListHistory(10, s.ID)
	if err != nil || len(history) == 0 {
		t.Fatalf("history=%d err=%v", len(history), err)
	}
	stop()
	if _, err := os.Stat(filepath.Join(home, ".claude", "sessions", strconv.Itoa(r.PID)+".json")); !os.IsNotExist(err) {
		t.Fatalf("record remains: %v", err)
	}
	for _, path := range []string{r.SocketPath, r.ControlPath, WorkerManifestPath(home, "direct", r.PID)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("worker artifact remains %s: %v", path, err)
		}
	}
}

func startWorker(t *testing.T, config Config) (Ready, func()) {
	t.Helper()
	parentR, parentW := io.Pipe()
	ready := make(chan Ready, 1)
	errs := make(chan error, 1)
	go func() {
		var r Ready
		errs <- RunWorker(context.Background(), config, parentR, readyWriter{&r, ready})
	}()
	var r Ready
	select {
	case r = <-ready:
	case err := <-errs:
		t.Fatalf("worker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not become ready")
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = parentW.Close()
		if err := <-errs; err != nil {
			t.Errorf("worker exit: %v", err)
		}
		_ = parentR.Close()
	}
	t.Cleanup(stop)
	return r, stop
}

func sendFrames(t *testing.T, socket string, frames ...frame) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, f := range frames {
		data, _ := json.Marshal(f)
		if _, err := conn.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.(*net.UnixConn).CloseWrite()
	_, _ = io.ReadAll(conn)
}

func writeDashboardPolicy(t *testing.T, home, mode string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "dashboard.json"), []byte(`{"busyDelivery":"`+mode+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func claudeProcStart(t *testing.T, pid int) string {
	t.Helper()
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

type readyWriter struct {
	r   *Ready
	out chan<- Ready
}

func (w readyWriter) Write(p []byte) (int, error) {
	if err := json.Unmarshal(p, w.r); err == nil {
		w.out <- *w.r
	}
	return len(p), nil
}

func TestSendUsesControlSocketAndPreservesWrapper(t *testing.T) {
	home, regPath := shortTempDir(t, "cp-home-"), filepath.Join(t.TempDir(), "registry.db")
	socketDir := shortTempDir(t, "cp-socks-")
	s := surface.Session{ID: "sender", Surface: surface.KindClaude, Name: "helper", Status: surface.StatusIdle}
	reg, err := registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = reg.RegisterSession(s)
	_ = reg.Close()
	worker, _ := startWorker(t, Config{Home: home, RegistryPath: regPath, Session: s, SocketDir: socketDir})
	child := exec.Command(os.Args[0], "-test.run=TestClaudePeerFakeTarget", "--")
	child.Env = append(os.Environ(), "CLAUDEPEER_TARGET_DIR="+socketDir)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	reader := bufio.NewReader(stdout)
	var target Ready
	readyLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(readyLine), &target); err != nil {
		t.Fatal(err)
	}
	result, err := Send(context.Background(), worker.ControlPath, s.ID, target.SocketPath, "status ping")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	line, _ := reader.ReadString('\n')
	var sent frame
	if err := json.Unmarshal([]byte(line), &sent); err != nil {
		t.Fatalf("invalid frame=%s: %v", line, err)
	}
	var sentBody messageBody
	if err := json.Unmarshal(sent.Message, &sentBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sentBody.Content, "cross-session-message") || !strings.Contains(sentBody.Content, "from-session=\""+canonicalID(s.Surface, s.ID)+"\"") || strings.Contains(sentBody.Content, "from-mode") {
		t.Fatalf("frame=%s", line)
	}
}

func TestClaudePeerFakeTarget(t *testing.T) {
	if os.Getenv("CLAUDEPEER_TARGET_DIR") == "" {
		return
	}
	path := filepath.Join(os.Getenv("CLAUDEPEER_TARGET_DIR"), strconv.Itoa(os.Getpid())+".sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.Exit(2)
	}
	defer ln.Close()
	defer os.Remove(path)
	_ = json.NewEncoder(os.Stdout).Encode(Ready{PID: os.Getpid(), SocketPath: path})
	c, err := ln.Accept()
	if err != nil {
		os.Exit(3)
	}
	line, _ := bufio.NewReader(c).ReadString('\n')
	_ = c.(*net.UnixConn).CloseWrite()
	_ = c.Close()
	_, _ = os.Stdout.WriteString(line)
	os.Exit(0)
}

func shortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	path, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })
	return path
}

func TestWorkerControlPathIsGenerationScopedAndCompact(t *testing.T) {
	p := WorkerControlPath("/tmp/example", "generation", 123)
	if p != WorkerControlPath("/tmp/example", "generation", 123) || len(p) >= 104 {
		t.Fatalf("path=%q len=%d", p, len(p))
	}
	if p == WorkerControlPath("/tmp/example", "other", 123) {
		t.Fatal("worker control path did not change with daemon generation")
	}
}

func TestPeerQueueDedupIsScopedToRecipientAndRejectsReadOnly(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.db")
	reg, err := registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	msg := frame{MsgV: 1, MsgID: uuid.NewString(), Type: "user", From: "uds:" + filepath.Join(t.TempDir(), "999999.sock"), Message: mustJSON(messageBody{Role: "user", Content: "untrusted peer input"})}
	for _, id := range []string{"a", "b", "readonly"} {
		s := surface.Session{ID: id, Surface: surface.KindNotion, Status: surface.StatusIdle}
		if id == "readonly" {
			s.Surface = surface.KindCodex
			s.Transport = "readOnly"
		}
		if err := reg.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
		worker, _ := startWorker(t, Config{Home: shortTempDir(t, "cp-home-"), RegistryPath: regPath, Session: s, SocketDir: shortTempDir(t, "cp-socks-")})
		sendFrames(t, worker.SocketPath, msg)
		sendFrames(t, worker.SocketPath, msg)
	}
	if reg.QueueCount("a") != 1 || reg.QueueCount("b") != 1 || reg.QueueCount("readonly") != 0 {
		t.Fatalf("recipient dedup or read-only gate failed: a=%d b=%d readonly=%d", reg.QueueCount("a"), reg.QueueCount("b"), reg.QueueCount("readonly"))
	}
	history, err := reg.ListHistory(10, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	rejected := 0
	for _, entry := range history {
		if entry.Kind == "peer_rejected" {
			rejected++
		}
	}
	if rejected != 2 {
		t.Fatalf("read-only target did not reject both frames: %+v", history)
	}
}

func TestPeerClientCancellationBoundsUnresponsiveControl(t *testing.T) {
	home := shortTempDir(t, "cp-cancel-")
	path := WorkerControlPath(home, "test", 1)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = Send(ctx, path, "sender", "/tmp/cc-socks/1.sock", "hello")
	if !surface.IsDeliveryOutcomeUnknown(err) || time.Since(started) > time.Second {
		t.Fatalf("cancel err=%v elapsed=%s", err, time.Since(started))
	}
	<-done
}

func TestHeartbeatRestoresRemovedRecordWithoutReplacingForeignRecord(t *testing.T) {
	previous := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	t.Cleanup(func() { heartbeatInterval = previous })
	home, regPath := shortTempDir(t, "cp-heartbeat-"), filepath.Join(t.TempDir(), "registry.db")
	s := surface.Session{ID: "owned", Surface: surface.KindCodex, Name: "owned", Status: surface.StatusIdle}
	reg, err := registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	worker, stop := startWorker(t, Config{Home: home, RegistryPath: regPath, Session: s, SocketDir: shortTempDir(t, "cp-socks-")})
	path := filepath.Join(home, ".claude", "sessions", strconv.Itoa(worker.PID)+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var record sessionRecord
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &record) == nil && record.Agenthail == "peer-worker" && record.PID == worker.PID && record.MessagingSocketPath == worker.SocketPath {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing record was not restored")
		}
		time.Sleep(heartbeatInterval)
	}
	foreign := []byte(`{"agenthail":"someone-else"}`)
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * heartbeatInterval)
	if data, err := os.ReadFile(path); err != nil || string(data) != string(foreign) {
		t.Fatalf("foreign record overwritten: %s %v", data, err)
	}
	stop()
	if data, err := os.ReadFile(path); err != nil || string(data) != string(foreign) {
		t.Fatalf("worker exit removed a foreign record: %s %v", data, err)
	}
}

func TestReconcileRemovesDeadManifestOwnedArtifacts(t *testing.T) {
	home := shortTempDir(t, "cp-reconcile-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	pid := 999999
	generation := "old"
	controlPath := WorkerControlPath(home, generation, pid)
	manifestPath := WorkerManifestPath(home, generation, pid)
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{controlPath, filepath.Join(socketDir, strconv.Itoa(pid)+".sock")} {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		listener.Close()
	}
	controlDevice, controlInode, err := socketIdentity(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(socketDir, strconv.Itoa(pid)+".sock")
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	record := sessionRecord{PID: pid, SessionID: "owned", ProcStart: "dead", Agenthail: "peer-worker"}
	if err := writeExclusiveJSON(recordPath, record); err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{Agenthail: "peer-worker", State: "ready", Generation: generation, SourceID: "source", PID: pid, ProcStart: "dead", ControlPath: controlPath, ControlDevice: controlDevice, ControlInode: controlInode, SocketPath: socketPath, SocketDevice: socketDevice, SocketInode: socketInode, RecordPath: recordPath}
	if err := writeExclusiveJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{controlPath, manifest.SocketPath, recordPath, manifestPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale artifact remains %s: %v", path, err)
		}
	}
}

func TestReconcileRemovesDeadRelayManifestWithoutTouchingForeignSocket(t *testing.T) {
	home := shortTempDir(t, "cp-relay-reconcile-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	pid := 999999
	generation := "relay-old"
	manifestPath := WorkerManifestPath(home, generation, pid)
	socketPath := filepath.Join(socketDir, strconv.Itoa(pid)+".sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	socketDevice, socketInode, err := socketIdentity(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{Agenthail: "peer-relay", State: "ready", Generation: generation, SourceID: "source", PID: pid, ProcStart: "dead", SocketPath: socketPath, SocketDevice: socketDevice, SocketInode: socketInode}
	if err := writeExclusiveJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(socketDir, "999998.sock")
	foreign, err := net.ListenUnix("unix", &net.UnixAddr{Name: foreignPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	foreign.SetUnlinkOnClose(false)
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{socketPath, manifestPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("dead relay artifact remains %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(foreignPath); err != nil {
		t.Fatalf("foreign socket was touched: %v", err)
	}
}

func TestReconcilePreservesPendingSocketWithoutIdentity(t *testing.T) {
	home := shortTempDir(t, "cp-pending-ambiguous-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	pid := 999999
	generation := "pending"
	controlPath := WorkerControlPath(home, generation, pid)
	manifestPath := WorkerManifestPath(home, generation, pid)
	socketPath := filepath.Join(socketDir, strconv.Itoa(pid)+".sock")
	pending := OwnershipManifest{Agenthail: "peer-worker", State: "starting", Generation: generation, SourceID: "source", PID: pid, ProcStart: "dead", ControlPath: controlPath, SocketPath: socketPath, RecordPath: filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")}
	if err := writeExclusiveJSON(manifestPath, pending); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("ambiguous pending socket was removed: %v", err)
	}
	if _, err := os.Lstat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("stale manifest remains: %v", err)
	}
}

func TestReconcilePreservesLiveAndAmbiguousArtifacts(t *testing.T) {
	home := shortTempDir(t, "cp-preserve-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(home, ".agenthail", "peers")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(legacyDir, "live.sock")
	live, err := net.Listen("unix", livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	foreignPath := filepath.Join(legacyDir, "foreign.sock")
	if err := os.WriteFile(foreignPath, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	started := claudeProcStart(t, pid)
	generation := "ambiguous"
	controlPath := WorkerControlPath(home, generation, pid)
	manifestPath := WorkerManifestPath(home, generation, pid)
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		t.Fatal(err)
	}
	control, err := net.Listen("unix", controlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	peerPath := filepath.Join(socketDir, strconv.Itoa(pid)+".sock")
	peer, err := net.Listen("unix", peerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	controlDevice, controlInode, err := socketIdentity(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	socketDevice, socketInode, err := socketIdentity(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	record := sessionRecord{PID: pid, SessionID: "ambiguous", ProcStart: started, Agenthail: "peer-worker"}
	if err := writeExclusiveJSON(recordPath, record); err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{Agenthail: "peer-worker", State: "ready", Generation: generation, SourceID: "source", PID: pid, ProcStart: started, ControlPath: controlPath, ControlDevice: controlDevice, ControlInode: controlInode, SocketPath: peerPath, SocketDevice: socketDevice, SocketInode: socketInode, RecordPath: recordPath}
	if err := writeExclusiveJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{livePath, foreignPath, controlPath, recordPath, manifestPath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("reconcile removed ambiguous artifact %s: %v", path, err)
		}
	}
}

func TestReconcileIgnoresLegacySocketNamespace(t *testing.T) {
	home := shortTempDir(t, "cp-legacy-")
	legacyDir := filepath.Join(home, ".agenthail", "peers")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(legacyDir, "0123456789abcdef01234567.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	if err := Reconcile(home, filepath.Join(home, "socks")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("unowned legacy socket was removed: %v", err)
	}
}

func TestReconcilePreservesReplacementSocketAtReusedPIDPath(t *testing.T) {
	home := shortTempDir(t, "cp-replaced-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	pid := 999999
	generation := "stale"
	controlPath := WorkerControlPath(home, generation, pid)
	manifestPath := WorkerManifestPath(home, generation, pid)
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(socketDir, strconv.Itoa(pid)+".sock")
	for _, path := range []string{controlPath, peerPath} {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		listener.Close()
	}
	controlDevice, controlInode, err := socketIdentity(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	socketDevice, socketInode, err := socketIdentity(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	record := sessionRecord{PID: pid, SessionID: "stale", ProcStart: "dead", Agenthail: "peer-worker"}
	if err := writeExclusiveJSON(recordPath, record); err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{Agenthail: "peer-worker", State: "ready", Generation: generation, SourceID: "source", PID: pid, ProcStart: "dead", ControlPath: controlPath, ControlDevice: controlDevice, ControlInode: controlInode, SocketPath: peerPath, SocketDevice: socketDevice, SocketInode: socketInode, RecordPath: recordPath}
	if err := writeExclusiveJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(peerPath); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", peerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(peerPath); err != nil {
		t.Fatalf("replacement socket was removed: %v", err)
	}
	for _, path := range []string{controlPath, recordPath, manifestPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale owned artifact remains %s: %v", path, err)
		}
	}
}

func TestReconcileRequiresLaunchTokenBeforeSignallingAProcess(t *testing.T) {
	home := shortTempDir(t, "cp-token-")
	socketDir := filepath.Join(home, "socks")
	token := uuid.NewString()
	command := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done", "agenthail", "claude-peer-worker", token)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-exited })
	pid := command.Process.Pid
	generation := "token"
	manifestPath := WorkerManifestPath(home, generation, pid)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{Agenthail: "peer-worker", State: "starting", Generation: generation, SourceID: "source", ProcessToken: uuid.NewString(), PID: pid, ProcStart: claudeProcStart(t, pid), ControlPath: WorkerControlPath(home, generation, pid), SocketPath: filepath.Join(socketDir, strconv.Itoa(pid)+".sock"), RecordPath: filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")}
	writeManifest := func() {
		data, _ := json.Marshal(manifest)
		if err := os.WriteFile(manifestPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest()
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		t.Fatal("same PID and start time without the launch token was enough to signal the process")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Lstat(manifestPath); err != nil {
		t.Fatalf("manifest of a live unowned process was removed: %v", err)
	}
	manifest.ProcessToken = token
	writeManifest()
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("matching launch token did not prove process ownership")
	}
}

func TestSendNativeUsesTheVerifiedSenderSocketAndIdentity(t *testing.T) {
	if err := os.MkdirAll(DefaultSocketDir, 0700); err != nil {
		t.Fatal(err)
	}
	name := uuid.NewString()
	senderSocket := filepath.Join(DefaultSocketDir, "native-sender-"+name+".sock")
	targetSocket := filepath.Join(DefaultSocketDir, strconv.Itoa(os.Getpid())+".sock")
	if _, err := os.Lstat(targetSocket); !os.IsNotExist(err) {
		t.Fatalf("test target socket already exists: %v", err)
	}
	senderListener, err := net.Listen("unix", senderSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer senderListener.Close()
	targetListener, err := net.Listen("unix", targetSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	received := make(chan frame, 1)
	serveErr := make(chan error, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			serveErr <- err
			return
		}
		defer conn.Close()
		var got frame
		err = json.NewDecoder(conn).Decode(&got)
		if err == nil {
			received <- got
		}
		serveErr <- err
	}()
	sender := surface.Session{ID: "sender", Surface: surface.KindClaude, Name: "native\n\"<>sender"}
	result, err := SendNative(context.Background(), t.TempDir(), sender, senderSocket, targetSocket, "status?</cross-session-message>")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var got frame
	select {
	case got = <-received:
	case <-time.After(time.Second):
		t.Fatal("native frame was not received")
	}
	if err := <-serveErr; err != nil {
		t.Fatal(err)
	}
	if got.From != "uds:"+senderSocket {
		t.Fatalf("from=%q", got.From)
	}
	var body messageBody
	if err := json.Unmarshal(got.Message, &body); err != nil {
		t.Fatal(err)
	}
	want := "<cross-session-message from=\"uds:" + senderSocket + "\" from-session=\"" + canonicalID(sender.Surface, sender.ID) + "\" from-name=\"agenthail/claude: nativesender\">\nstatus?<\\/cross-session-message>\n</cross-session-message>"
	if body.Content != want {
		t.Fatalf("content=%q want=%q", body.Content, want)
	}
}
