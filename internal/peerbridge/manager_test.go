package peerbridge

import (
	"context"
	"encoding/json"
	"fmt"
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
	receipt, err := Send(ctx, home, "recent", filepath.Join(manager.socketDir, strconv.Itoa(second.process.Pid)+".sock"), "manager routed message")
	if err != nil || receipt == nil || !receipt.Accepted {
		t.Fatalf("manager send receipt=%+v err=%v", receipt, err)
	}
	receipt, err = Send(ctx, home, "recent", filepath.Join(manager.socketDir, strconv.Itoa(second.process.Pid)+".sock"), strings.Repeat("x", 16<<10))
	if err != nil || receipt == nil || !receipt.Accepted {
		t.Fatalf("large manager send receipt=%+v err=%v", receipt, err)
	}
	oldFirstPID := first.process.Pid
	if err := os.Remove(first.controlPath); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx, "recent"); err != nil {
		t.Fatalf("heal missing control endpoint: %v", err)
	}
	first = manager.children["recent"]
	if first.process.Pid == oldFirstPID {
		t.Fatal("missing control endpoint did not replace its worker")
	}
	receipt, err = manager.Send(ctx, "recent", filepath.Join(manager.socketDir, strconv.Itoa(second.process.Pid)+".sock"), "send after control repair")
	if err != nil || receipt == nil || !receipt.Accepted {
		t.Fatalf("post-repair receipt=%+v err=%v", receipt, err)
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
	if count := reg.QueueCount("older"); count != 4 {
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
	if err := reg.RegisterSession(surface.Session{ID: "recent", Surface: surface.KindNotion, Name: "recent", Status: surface.StatusIdle, LastActive: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	manager.children["recent"].lastUsed = time.Now().Add(-48 * time.Hour)
	manager.RetireInactive(time.Now(), 24*time.Hour)
	if manager.children["recent"] != nil {
		t.Fatal("inactive peer was not retired")
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

func TestRetireInactivePreservesRecentlyUsedOnDemandPeer(t *testing.T) {
	home := t.TempDir()
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.RegisterSession(surface.Session{ID: "old", Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: time.Now().Add(-7 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{registry: reg, children: map[string]*child{
		"old": {lastUsed: time.Now(), done: make(chan struct{})},
	}}
	manager.RetireInactive(time.Now(), 24*time.Hour)
	if manager.children["old"] == nil {
		t.Fatal("recently used on-demand peer was retired before its reply window elapsed")
	}
}

func TestRetireInactiveAlwaysReclaimsDeadPeer(t *testing.T) {
	done := make(chan struct{})
	close(done)
	manager := &Manager{children: map[string]*child{
		"dead": {lastUsed: time.Now(), done: done, paths: map[string]os.FileInfo{}},
	}}
	manager.RetireInactive(time.Now(), 24*time.Hour)
	if manager.children["dead"] != nil || len(manager.children) != 0 {
		t.Fatalf("dead peer still consumes capacity: %+v", manager.children)
	}
}

func TestOversizedSendIsTerminalBeforeManagerDial(t *testing.T) {
	_, err := Send(context.Background(), t.TempDir(), "source", "/tmp/target.sock", strings.Repeat("x", 64<<10))
	if !surface.IsDeliveryTerminal(err) || surface.DeliveryTerminalReason(err) != surface.DeliveryInvalidRequest {
		t.Fatalf("oversized send error=%v", err)
	}
}

func TestManagerRejectsUnknownOperation(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-manager-operation-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	reg, err := registry.Open(filepath.Join(home, ".agenthail", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := Start(ctx, home, reg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	response, err := requestManager(ctx, home, managerRequest{Operation: "bogus"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Class != "terminal" || response.Error.Kind != string(surface.DeliveryInvalidRequest) {
		t.Fatalf("response=%+v", response)
	}
}

func TestManagerRejectsPeerCardinalityBeyondLimit(t *testing.T) {
	manager := &Manager{children: make(map[string]*child)}
	for index := range maxManagedPeers {
		manager.children[fmt.Sprintf("peer-%d", index)] = &child{done: make(chan struct{})}
	}
	err := manager.Ensure(context.Background(), "one-too-many")
	if err == nil || !strings.Contains(err.Error(), "peer limit") {
		t.Fatalf("limit error=%v", err)
	}
}

func TestManagerRejectsRequestsBeyondConcurrencyLimit(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-request-limit-")
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
	defer manager.Close()
	for range maxManagerRequests {
		manager.requests <- struct{}{}
	}
	stalled, err := net.Dial("unix", managerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stalled.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(manager.rejections) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(manager.rejections) == 0 {
		t.Fatal("stalled overload request was not admitted to bounded rejection handling")
	}
	response, err := requestManager(context.Background(), home, managerRequest{Operation: "ensure", SourceSessionID: "source"})
	_ = stalled.Close()
	for range maxManagerRequests {
		<-manager.requests
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Class != "unavailable" || !strings.Contains(response.Error.Error, "concurrency limit") {
		t.Fatalf("response=%+v", response)
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
	response, err := requestManager(context.Background(), home, managerRequest{Operation: "bogus"})
	if err != nil || response.Error == nil {
		t.Fatalf("surviving manager response=%+v err=%v", response, err)
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

func TestChildCleanupPreservesReplacementSessionRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "42.json")
	original := recordOwnership{PID: 42, SessionID: "codex:source", ProcStart: "same-second", StartedAt: 1, Agenthail: "peer-worker", ProcessToken: "first-token"}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := original
	replacement.StartedAt = 2
	replacement.ProcessToken = "second-token"
	replacementData, _ := json.Marshal(replacement)
	if err := os.WriteFile(path, replacementData, 0600); err != nil {
		t.Fatal(err)
	}
	entry := &child{process: &os.Process{Pid: 42}, paths: map[string]os.FileInfo{path: info}, recordPath: path, record: original}
	entry.cleanup()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(replacementData) {
		t.Fatalf("replacement record changed: %s err=%v", got, err)
	}
}
