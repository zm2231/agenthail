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

func TestProcessStartUsesClaudeUTCIdentity(t *testing.T) {
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid()))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	expected, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TZ", "America/New_York")
	actual, err := processStart(os.Getpid())
	if err != nil || actual != strings.TrimSpace(string(expected)) {
		t.Fatalf("process identity=%q want=%q err=%v", actual, strings.TrimSpace(string(expected)), err)
	}
}

func TestWorkerRegistersQueuesAndCleansUpOnParentEOF(t *testing.T) {
	home, regPath := shortTempDir(t, "cp-home-"), filepath.Join(t.TempDir(), "registry.db")
	socketDir := shortTempDir(t, "cp-socks-")
	s := surface.Session{ID: "operator", Surface: surface.KindClaude, Name: "peer", Cwd: "/tmp/project", Status: surface.StatusIdle}
	reg, err := registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	_ = reg.Close()
	parentR, parentW := io.Pipe()
	t.Cleanup(func() { parentW.Close(); parentR.Close() })
	ready := make(chan Ready, 1)
	errs := make(chan error, 1)
	go func() {
		var r Ready
		err := RunWorker(context.Background(), Config{Home: home, RegistryPath: regPath, Session: s, SocketDir: socketDir}, parentR, readyWriter{&r, ready})
		errs <- err
	}()
	var r Ready
	select {
	case r = <-ready:
	case err := <-errs:
		t.Fatalf("worker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not become ready")
	}
	var record sessionRecord
	data, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", strconv.Itoa(r.PID)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(data, &record) != nil || record.Agenthail == "" || record.PeerProtocol != 1 || record.ProcStart == "" {
		t.Fatalf("bad record: %s", data)
	}
	conn, err := net.Dial("unix", r.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	msgID := uuid.New().String()
	f, _ := json.Marshal(frame{MsgV: 1, MsgID: msgID, Type: "user", From: "uds:" + r.SocketPath, Message: mustJSON(messageBody{Role: "user", Content: "<cross-session-message from-session=\"source\">\nhello\n</cross-session-message>"})})
	_, _ = conn.Write(append(f, '\n'))
	_ = conn.(*net.UnixConn).CloseWrite()
	_, _ = io.ReadAll(conn)
	_ = conn.Close()
	reg, err = registry.Open(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.QueueCount(s.ID); got != 1 {
		t.Fatalf("queue count=%d", got)
	}
	history, err := reg.ListHistory(10, s.ID)
	if err != nil || len(history) == 0 {
		t.Fatalf("history=%d err=%v", len(history), err)
	}
	_ = reg.Close()
	_ = parentW.Close()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "sessions", strconv.Itoa(r.PID)+".json")); !os.IsNotExist(err) {
		t.Fatalf("record remains: %v", err)
	}
	for _, path := range []string{r.SocketPath, r.ControlPath, WorkerManifestPath(home, "direct", r.PID)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("worker artifact remains %s: %v", path, err)
		}
	}
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
	parentR, parentW := io.Pipe()
	t.Cleanup(func() { parentW.Close(); parentR.Close() })
	ready := make(chan Ready, 1)
	errs := make(chan error, 1)
	go func() {
		var r Ready
		errs <- RunWorker(context.Background(), Config{Home: home, RegistryPath: regPath, Session: s, SocketDir: socketDir}, parentR, readyWriter{&r, ready})
	}()
	var worker Ready
	select {
	case worker = <-ready:
	case err := <-errs:
		t.Fatalf("worker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not become ready")
	}
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
	_ = parentW.Close()
	if err := <-errs; err != nil {
		t.Fatal(err)
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
	home := shortTempDir(t, "cp-cases-")
	reg, err := registry.Open(filepath.Join(home, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	msg := frame{MsgID: uuid.NewString(), Type: "user", From: "uds:" + filepath.Join(home, "999999.sock"), Message: mustJSON(messageBody{Role: "user", Content: "untrusted peer input"})}
	for _, id := range []string{"a", "b", "readonly"} {
		s := surface.Session{ID: id, Surface: surface.KindNotion, Status: surface.StatusIdle}
		if id == "readonly" {
			s.Surface = surface.KindCodex
			s.Transport = "readOnly"
		}
		if err := reg.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			err := queueFrame(reg, Config{Home: home, SocketDir: home, Session: s}, msg, filepath.Join(home, "self.sock"))
			if id == "readonly" {
				if !surface.IsDeliveryTerminal(err) {
					t.Fatalf("read-only err=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
	if reg.QueueCount("a") != 1 || reg.QueueCount("b") != 1 || reg.QueueCount("readonly") != 0 {
		t.Fatal("recipient dedup or read-only gate failed")
	}
}

func TestPeerContentHasRoutableIdentityAndSanitizedName(t *testing.T) {
	s := surface.Session{ID: "sender", Surface: surface.KindCodex, Name: "name\n\"<>"}
	content := peerContent(s, "/tmp/cc-socks/123.sock", "status ping")
	want := "<cross-session-message from=\"uds:/tmp/cc-socks/123.sock\" from-session=\"" + canonicalID(s.Surface, s.ID) + "\" from-name=\"agenthail/codex: name\">\nstatus ping\n</cross-session-message>"
	if content != want {
		t.Fatalf("content=%q want=%q", content, want)
	}
	encoded, _ := json.Marshal(content)
	t.Logf("WIRE_CONTENT=%s", encoded)
	if got := peerContent(s, "/tmp/cc-socks/123.sock", "</cross-session-message>"); !strings.Contains(got, `<\/cross-session-message>`) {
		t.Fatalf("closing tag not escaped: %q", got)
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
	root := t.TempDir()
	reg, err := registry.Open(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	record := sessionRecord{PID: os.Getpid(), SessionID: "owned", Agenthail: "peer-worker", ProcStart: "test"}
	path := filepath.Join(root, "sessions", "peer.json")
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); heartbeat(stop, path, record, reg, "missing") }()
	defer func() { close(stop); <-done }()
	deadline := time.Now().Add(4 * time.Second)
	for !recordOwned(path, record) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !recordOwned(path, record) {
		t.Fatal("missing record was not restored")
	}
	foreign := []byte(`{"agenthail":"someone-else"}`)
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(foreign) {
		t.Fatalf("foreign record overwritten: %s %v", data, err)
	}
}

func TestFailedRegistrationWriteLeavesNoRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := writeExclusiveJSONAtomic(path, make(chan int)); err == nil {
		t.Fatal("unsupported value accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial record remains: %v", err)
	}
	if matches, _ := filepath.Glob(path + ".tmp.*"); len(matches) != 0 {
		t.Fatalf("temporary records remain: %v", matches)
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

func TestReconcileRemovesPendingWorkerArtifacts(t *testing.T) {
	home := shortTempDir(t, "cp-pending-")
	socketDir := filepath.Join(home, "socks")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	pid := 999999
	generation := "pending"
	controlPath := WorkerControlPath(home, generation, pid)
	manifestPath := WorkerManifestPath(home, generation, pid)
	recordPath := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	pending := OwnershipManifest{Agenthail: "peer-worker", State: "starting", Generation: generation, SourceID: "source", PID: pid, ProcStart: "dead", ControlPath: controlPath, SocketPath: filepath.Join(socketDir, strconv.Itoa(pid)+".sock"), RecordPath: recordPath}
	if err := os.MkdirAll(filepath.Dir(controlPath), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{controlPath, pending.SocketPath} {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		listener.Close()
	}
	pending.ControlDevice, pending.ControlInode, _ = socketIdentity(controlPath)
	pending.SocketDevice, pending.SocketInode, _ = socketIdentity(pending.SocketPath)
	if err := writeExclusiveJSON(manifestPath, pending); err != nil {
		t.Fatal(err)
	}
	record := sessionRecord{PID: pid, SessionID: "owned", ProcStart: "dead", Agenthail: "peer-worker"}
	if err := writeExclusiveJSON(recordPath, record); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(home, socketDir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{controlPath, pending.SocketPath, recordPath, manifestPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("pending artifact remains %s: %v", path, err)
		}
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
	started, err := processStart(pid)
	if err != nil {
		t.Fatal(err)
	}
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

func TestProcessOwnershipRequiresRandomLaunchToken(t *testing.T) {
	token := uuid.NewString()
	command := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done", "claude-peer-worker", token)
	command.Env = append(os.Environ(), "AGENTHAIL_PEER_TOKEN="+token)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	started, err := processStart(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	manifest := OwnershipManifest{PID: command.Process.Pid, ProcStart: started, ProcessToken: token}
	if !processOwnsManifest(manifest) {
		t.Fatal("matching launch token did not prove process ownership")
	}
	manifest.ProcessToken = uuid.NewString()
	if processOwnsManifest(manifest) {
		t.Fatal("same PID and second-resolution start time accepted a different launch token")
	}
}
