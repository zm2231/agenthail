package peerbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var (
	buildOnce sync.Once
	buildDir  string
	binary    string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

func agenthailBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "ah-bin-")
		if buildErr != nil {
			return
		}
		binary = filepath.Join(buildDir, "agenthail")
		if output, err := exec.Command("go", "build", "-o", binary, "../../cmd/agenthail").CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build: %v\n%s", err, output)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binary
}

type peerFixture struct {
	t         *testing.T
	ctx       context.Context
	home      string
	socketDir string
	reg       *registry.Registry
	manager   *Manager
}

type peerRecord struct {
	PID                 int    `json:"pid"`
	Name                string `json:"name"`
	Agenthail           string `json:"agenthail"`
	MessagingSocketPath string `json:"messagingSocketPath"`
}

func newPeerFixture(t *testing.T, sessionIDs ...string) *peerFixture {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-peers-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	for _, id := range sessionIDs {
		if err := reg.RegisterSession(surface.Session{ID: id, Surface: surface.KindNotion, Name: id, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	socketDir := filepath.Join(home, "socks")
	manager, err := start(ctx, home, reg, agenthailBinary(t), socketDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return &peerFixture{t: t, ctx: ctx, home: home, socketDir: socketDir, reg: reg, manager: manager}
}

func (f *peerFixture) records() []peerRecord {
	f.t.Helper()
	paths, _ := filepath.Glob(filepath.Join(f.home, ".claude", "sessions", "*.json"))
	var records []peerRecord
	for _, path := range paths {
		var record peerRecord
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &record) == nil && record.Agenthail == "peer-worker" {
			records = append(records, record)
		}
	}
	return records
}

func (f *peerFixture) worker(name string) peerRecord {
	f.t.Helper()
	var found []peerRecord
	for _, record := range f.records() {
		if record.Name == "agenthail/notion: "+name {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		f.t.Fatalf("Claude-visible records for %q: %+v", name, found)
	}
	return found[0]
}

func (f *peerFixture) recordPath(pid int) string {
	return filepath.Join(f.home, ".claude", "sessions", strconv.Itoa(pid)+".json")
}

func (f *peerFixture) controlSockets(pid int) []string {
	paths, _ := filepath.Glob(filepath.Join(claudepeer.RuntimeRoot(f.home), "*", strconv.Itoa(pid)+".sock"))
	return paths
}

func killProcess(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, fmt.Sprintf("process %d to exit", pid), func() bool { return syscall.Kill(pid, 0) != nil })
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sendPeerFrame(t *testing.T, socket string, frame map[string]any) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		t.Fatal(err)
	}
	_ = conn.(*net.UnixConn).CloseWrite()
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatal(err)
	}
}

func userFrame(id, from, content string) map[string]any {
	return map[string]any{"msgV": 1, "msg_id": id, "type": "user", "from": "uds:" + from, "message": map[string]string{"role": "user", "content": content}}
}

type nativePeer struct {
	socket string
	frames chan map[string]any
}

func (f *peerFixture) startNative() *nativePeer {
	f.t.Helper()
	t := f.t
	if err := os.MkdirAll(f.socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	native := &nativePeer{socket: filepath.Join(f.socketDir, strconv.Itoa(os.Getpid())+".sock"), frames: make(chan map[string]any, 64)}
	listener, err := net.Listen("unix", native.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(native.socket) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			data, _ := io.ReadAll(conn)
			_ = conn.Close()
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var frame map[string]any
				if json.Unmarshal([]byte(line), &frame) == nil {
					native.frames <- frame
				}
			}
		}
	}()
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid()))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	started, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "native-test", "procStart": strings.TrimSpace(string(started)), "messagingSocketPath": native.socket})
	path := f.recordPath(os.Getpid())
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, record, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return native
}

func (n *nativePeer) await(t *testing.T, kind string) map[string]any {
	t.Helper()
	for {
		select {
		case frame := <-n.frames:
			if frame["type"] == kind {
				return frame
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("native peer received no %s frame", kind)
		}
	}
}

func (n *nativePeer) replySocket(t *testing.T) string {
	t.Helper()
	from, _ := n.await(t, "user")["from"].(string)
	if !strings.HasPrefix(from, "uds:") {
		t.Fatalf("frame from=%q", from)
	}
	return strings.TrimPrefix(from, "uds:")
}

func TestManagerOwnsOnePeerPerSessionAndHealsLostWorkers(t *testing.T) {
	f := newPeerFixture(t, "recent", "older")
	if err := f.manager.Ensure(f.ctx, "recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.ReverseAlias("recent"); err != sql.ErrNoRows {
		t.Fatalf("background registration minted handle: %v", err)
	}
	if err := Ensure(f.ctx, f.home, "older"); err != nil {
		t.Fatal(err)
	}
	recent, older := f.worker("recent"), f.worker("older")
	if recent.PID == older.PID {
		t.Fatal("agents share a PID")
	}
	if err := Ensure(f.ctx, f.home, "older"); err != nil {
		t.Fatal(err)
	}
	if again := f.worker("older"); again.PID != older.PID {
		t.Fatalf("duplicate registration spawned a new worker: %d -> %d", older.PID, again.PID)
	}

	duplicate := userFrame(uuid.NewString(), recent.MessagingSocketPath, "peer reply")
	sendPeerFrame(t, older.MessagingSocketPath, duplicate)
	sendPeerFrame(t, older.MessagingSocketPath, duplicate)
	if count := f.reg.QueueCount("older"); count != 1 {
		t.Fatalf("duplicate inbound msg_id queued %d times", count)
	}

	if err := f.reg.SetAlias("renamed", "older"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "heartbeat to publish the rename", func() bool {
		var record peerRecord
		data, err := os.ReadFile(f.recordPath(older.PID))
		return err == nil && json.Unmarshal(data, &record) == nil && record.Name == "agenthail/notion: renamed"
	})

	controls := f.controlSockets(older.PID)
	if len(controls) != 1 {
		t.Fatalf("control sockets=%v", controls)
	}
	if err := os.Remove(controls[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(f.ctx, "older"); err != nil {
		t.Fatalf("heal missing control endpoint: %v", err)
	}
	healed := f.worker("renamed")
	if healed.PID == older.PID {
		t.Fatal("missing control endpoint did not replace its worker")
	}
	killProcess(t, healed.PID)
	if err := Ensure(f.ctx, f.home, "older"); err != nil {
		t.Fatalf("worker restart: %v", err)
	}
	if restarted := f.worker("renamed"); restarted.PID == healed.PID {
		t.Fatal("dead worker reused")
	}

	var pids []int
	for _, record := range f.records() {
		pids = append(pids, record.PID)
	}
	f.manager.Close()
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			t.Fatalf("worker %d remained alive", pid)
		}
	}
	for _, pattern := range []string{filepath.Join(f.socketDir, "*.sock"), filepath.Join(claudepeer.RuntimeRoot(f.home), "*", "*"), filepath.Join(f.home, ".claude", "sessions", "*.json")} {
		if files, _ := filepath.Glob(pattern); len(files) != 0 {
			t.Fatalf("peer artifacts remain: %v", files)
		}
	}
}

func TestManagerReplyRelayLifecycle(t *testing.T) {
	f := newPeerFixture(t, "recent")
	native := f.startNative()
	send := func(message string) {
		t.Helper()
		receipt, err := f.manager.Send(f.ctx, "recent", native.socket, message)
		if err != nil || receipt == nil || !receipt.Accepted {
			t.Fatalf("send receipt=%+v err=%v", receipt, err)
		}
	}

	receipt, err := Send(f.ctx, f.home, "recent", native.socket, "manager routed message")
	if err != nil || receipt == nil || !receipt.Accepted {
		t.Fatalf("manager send receipt=%+v err=%v", receipt, err)
	}
	if alias, err := f.reg.ReverseAlias("recent"); err != nil || alias != "recent" {
		t.Fatalf("first send handle=%q err=%v", alias, err)
	}
	relay := native.replySocket(t)
	helper := f.worker("recent")
	if relay == helper.MessagingSocketPath {
		t.Fatal("non-Claude sender replies route to its helper instead of a durable relay")
	}

	results := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := f.manager.Send(f.ctx, "recent", native.socket, "concurrent")
			results <- err
		}()
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent send: %v", err)
		}
	}
	for range 4 {
		if got := native.replySocket(t); got != relay {
			t.Fatalf("concurrent sends replaced the durable relay: %q -> %q", relay, got)
		}
	}
	send(strings.Repeat("x", 16<<10))
	if got := native.replySocket(t); got != relay {
		t.Fatalf("large send used relay %q, want %q", got, relay)
	}

	controls := f.controlSockets(helper.PID)
	if len(controls) != 1 {
		t.Fatalf("control sockets=%v", controls)
	}
	if err := os.Remove(controls[0]); err != nil {
		t.Fatal(err)
	}
	send("send after control repair")
	if got := native.replySocket(t); got != relay {
		t.Fatal("helper replacement discarded the durable relay")
	}
	replacementHelper := f.worker("recent")
	if replacementHelper.PID == helper.PID {
		t.Fatal("missing control endpoint did not replace its helper")
	}

	killProcess(t, replacementHelper.PID)
	recordPath := f.recordPath(replacementHelper.PID)
	var record map[string]any
	data, err := os.ReadFile(recordPath)
	if err != nil || json.Unmarshal(data, &record) != nil {
		t.Fatalf("dead helper record=%s err=%v", data, err)
	}
	record["processToken"] = "replacement-process"
	replacement, _ := json.Marshal(record)
	if err := os.WriteFile(recordPath, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	f.manager.RetireInactive(time.Now(), 24*time.Hour)
	if got, err := os.ReadFile(recordPath); err != nil || string(got) != string(replacement) {
		t.Fatalf("retiring a dead helper changed a replacement record: %s err=%v", got, err)
	}
	if controls := f.controlSockets(replacementHelper.PID); len(controls) != 0 {
		t.Fatalf("dead helper was not retired: %v", controls)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}

	replyID := uuid.NewString()
	reply := userFrame(replyID, native.socket, "reply after helper replacement")
	sendPeerFrame(t, relay, reply)
	sendPeerFrame(t, relay, reply)
	if count := f.reg.QueueCount("recent"); count != 1 {
		t.Fatalf("relay reply queue count=%d", count)
	}
	status := native.await(t, "control")
	if status["status"] != "received" || status["orig_msg_id"] != replyID {
		t.Fatalf("native receipt=%v", status)
	}

	relayPID, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(relay), ".sock"))
	if err != nil {
		t.Fatal(err)
	}
	killProcess(t, relayPID)
	send("send after relay restart")
	restarted := native.replySocket(t)
	if restarted == relay {
		t.Fatal("dead relay was reused")
	}
	if _, err := os.Lstat(relay); !os.IsNotExist(err) {
		t.Fatalf("old relay socket remains after restart: %v", err)
	}

	if err := f.reg.RegisterSession(surface.Session{ID: "recent", Surface: surface.KindNotion, Name: "recent", Status: surface.StatusOffline}); err != nil {
		t.Fatal(err)
	}
	f.manager.RetireInactive(time.Now().Add(48*time.Hour), 24*time.Hour)
	if _, err := os.Lstat(restarted); !os.IsNotExist(err) {
		t.Fatalf("offline relay was not retired: %v", err)
	}
	if records := f.records(); len(records) != 0 {
		t.Fatalf("offline helper was not retired: %+v", records)
	}
}

func TestOversizedSendIsTerminalBeforeManagerDial(t *testing.T) {
	_, err := Send(context.Background(), t.TempDir(), "source", "/tmp/target.sock", strings.Repeat("x", 64<<10))
	if !surface.IsDeliveryTerminal(err) || surface.DeliveryTerminalReason(err) != surface.DeliveryInvalidRequest {
		t.Fatalf("oversized send error=%v", err)
	}
}

type wireResponse struct {
	OK    bool `json:"ok"`
	Error *struct {
		Class string `json:"class"`
		Kind  string `json:"kind"`
		Error string `json:"error"`
	} `json:"error"`
}

func managerWireRequest(t *testing.T, home string, request map[string]any) wireResponse {
	t.Helper()
	conn, err := net.Dial("unix", managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response wireResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func startIdleManager(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-manager-wire-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	manager, err := Start(context.Background(), home, reg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return home
}

func TestManagerRejectsUnknownOperation(t *testing.T) {
	home := startIdleManager(t)
	response := managerWireRequest(t, home, map[string]any{"operation": "bogus"})
	if response.OK || response.Error == nil || response.Error.Class != "terminal" || response.Error.Kind != string(surface.DeliveryInvalidRequest) {
		t.Fatalf("response=%+v", response)
	}
}

func TestManagerRejectsRequestsBeyondConcurrencyLimit(t *testing.T) {
	home := startIdleManager(t)
	for range maxManagerRequests {
		stalled, err := net.Dial("unix", managerPath(home))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stalled.Close() })
	}
	response := managerWireRequest(t, home, map[string]any{"operation": "bogus"})
	if response.Error == nil || response.Error.Class != "unavailable" {
		t.Fatalf("request beyond the concurrency limit was processed: %+v", response)
	}
}

func TestEnsureReportsWorkerStartupDiagnostic(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-diagnostic-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	executable := filepath.Join(home, "broken-agenthail")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho 'worker bootstrap failed: marker-42' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.RegisterSession(surface.Session{ID: "source", Surface: surface.KindCodex, Status: surface.StatusBusy}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager, err := Start(ctx, home, reg, executable)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	err = manager.Ensure(ctx, "source")
	if err == nil || !strings.Contains(err.Error(), "marker-42") || strings.HasSuffix(err.Error(), "EOF") {
		t.Fatalf("startup error=%v", err)
	}
}

func TestManagerRestartReconcilesCrashArtifacts(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-crash-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	binary := agenthailBinary(t)
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: "source", Surface: surface.KindCodex, Name: "source", Status: surface.StatusBusy, LastActive: time.Now()}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	socketDir := filepath.Join(home, "socks")
	first, err := start(ctx, home, reg, binary, socketDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Ensure(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	crashed := first.children[session.ID]
	first.listener.Close()
	_ = os.Remove(managerPath(home))
	unlockManagerStartup(first.ownerLock)
	first.ownerLock = nil

	second, err := start(ctx, home, reg, binary, socketDir)
	if err != nil {
		t.Fatalf("restart after crash: %v", err)
	}
	defer second.Close()
	select {
	case <-crashed.done:
	case <-time.After(3 * time.Second):
		t.Fatal("orphaned worker survived manager reconciliation")
	}
	if err := second.Ensure(ctx, session.ID); err != nil {
		t.Fatalf("ensure after crash: %v", err)
	}
	if replacement := second.children[session.ID]; replacement == nil || replacement.process.Pid == crashed.process.Pid {
		t.Fatalf("replacement=%+v crashed pid=%d", replacement, crashed.process.Pid)
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

func TestManagerStartPreservesLiveEndpoint(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	if err := os.MkdirAll(filepath.Dir(managerPath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	before, err := os.Lstat(managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if _, err := Start(context.Background(), home, reg, "missing"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("start error=%v", err)
	}
	after, err := os.Lstat(managerPath(home))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("live manager endpoint was replaced: %v", err)
	}
}

func TestConcurrentManagerStartReplacesStaleEndpointOnce(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-race-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	if err := os.MkdirAll(filepath.Dir(managerPath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: managerPath(home), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	type result struct {
		manager *Manager
		err     error
	}
	ready := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-ready
			manager, err := Start(context.Background(), home, reg, "missing")
			results <- result{manager: manager, err: err}
		}()
	}
	close(ready)
	var winner *Manager
	started := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winner = result.manager
			started++
		}
	}
	if started != 1 {
		t.Fatalf("started managers=%d", started)
	}
	defer winner.Close()
	if response := managerWireRequest(t, home, map[string]any{"operation": "bogus"}); response.Error == nil {
		t.Fatalf("surviving manager response=%+v", response)
	}
}

func TestManagerLifetimeLockAndClosePreserveReplacementEndpoint(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-owner-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	manager, err := Start(context.Background(), home, reg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managerPath(home)); err != nil {
		t.Fatal(err)
	}
	if second, err := Start(context.Background(), home, reg, "missing"); err == nil {
		second.Close()
		t.Fatal("second manager started while the lifetime owner lock was held")
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: managerPath(home), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer replacement.Close()
	before, err := os.Lstat(managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	manager.Close()
	after, err := os.Lstat(managerPath(home))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("manager close removed replacement endpoint: %v", err)
	}
}

func TestSendResponseLossHasUnknownOutcome(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-response-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	if err := os.MkdirAll(filepath.Dir(managerPath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	read := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			var request managerRequest
			err = json.NewDecoder(conn).Decode(&request)
			_ = conn.Close()
		}
		read <- err
	}()
	_, err = Send(context.Background(), home, "source", "/tmp/target.sock", "accepted then response lost")
	if !surface.IsDeliveryOutcomeUnknown(err) || surface.IsDeliveryUnavailable(err) {
		t.Fatalf("response-loss error=%v", err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}
