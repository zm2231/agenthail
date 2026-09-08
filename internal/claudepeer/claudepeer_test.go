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
	select {
	case <-ready:
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
	result, err := Send(context.Background(), home, s.ID, target.SocketPath, "status ping")
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

func TestControlPathIsCompactAndDeterministic(t *testing.T) {
	p := ControlPath("/Users/example", "sender")
	if p != ControlPath("/Users/example", "sender") || len(p) >= 104 {
		t.Fatalf("path=%q len=%d", p, len(p))
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
	path := ControlPath(home, "sender")
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
	_, err = Send(ctx, home, "sender", "/tmp/cc-socks/1.sock", "hello")
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
	if err := writeExclusiveJSON(path, make(chan int)); err == nil {
		t.Fatal("unsupported value accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial record remains: %v", err)
	}
}
