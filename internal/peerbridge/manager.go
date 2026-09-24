package peerbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	OperatorID           = "agenthail-operator"
	managerRequestBytes  = 128 << 10
	managerReplyBytes    = 128 << 10
	workerReadyBytes     = 8 << 10
	maxManagedPeers      = 128
	maxManagerRequests   = 32
	maxManagerRejections = 8
)

type child struct {
	input       io.WriteCloser
	done        chan struct{}
	process     *os.Process
	paths       map[string]os.FileInfo
	controlPath string
	recordPath  string
	record      recordOwnership
	lastUsed    time.Time
}

type recordOwnership struct {
	PID          int    `json:"pid"`
	SessionID    string `json:"sessionId"`
	ProcStart    string `json:"procStart"`
	StartedAt    int64  `json:"startedAt"`
	Agenthail    string `json:"agenthail"`
	ProcessToken string `json:"processToken"`
}

type cappedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if len(data) < remaining {
			remaining = len(data)
		}
		b.data = append(b.data, data[:remaining]...)
	}
	return len(data), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

type managerRequest struct {
	Operation       string `json:"operation"`
	SourceSessionID string `json:"sourceSessionId"`
	TargetSocket    string `json:"targetSocket,omitempty"`
	Message         string `json:"message,omitempty"`
}

type managerError struct {
	Class string `json:"class"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error"`
}

type managerResponse struct {
	OK     bool                `json:"ok"`
	Result *surface.SendResult `json:"result,omitempty"`
	Error  *managerError       `json:"error,omitempty"`
}

type Manager struct {
	home       string
	socketDir  string
	registry   *registry.Registry
	executable string
	ctx        context.Context
	listener   net.Listener
	mu         sync.Mutex
	children   map[string]*child
	closed     bool
	generation string
	requests   chan struct{}
	rejections chan struct{}
	ownerLock  *os.File
	endpoint   os.FileInfo
}

func managerPath(home string) string {
	return filepath.Join(home, ".agenthail", "claude-peers.sock")
}

func Start(ctx context.Context, home string, reg *registry.Registry, executable string) (*Manager, error) {
	return start(ctx, home, reg, executable, "")
}

func start(ctx context.Context, home string, reg *registry.Registry, executable, socketDir string) (*Manager, error) {
	path := managerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	startupLock, err := lockManagerStartup(home)
	if err != nil {
		return nil, err
	}
	lockTransferred := false
	defer func() {
		if !lockTransferred {
			unlockManagerStartup(startupLock)
		}
	}()
	// The daemon lock prevents duplicate production owners; this narrower lock
	// also keeps direct callers from racing stale-endpoint replacement.
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("peer manager path is not a socket: %s", path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Getuid()) {
			return nil, fmt.Errorf("peer manager socket is not owned by the current user: %s", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("peer manager is already running")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("peer manager endpoint liveness is ambiguous: %w", dialErr)
		}
		current, currentErr := os.Lstat(path)
		if currentErr != nil || !os.SameFile(info, current) {
			return nil, fmt.Errorf("peer manager endpoint changed during startup")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := claudepeer.Reconcile(home, socketDir); err != nil {
		return nil, fmt.Errorf("reconcile Claude peers: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	endpoint, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return nil, err
	}
	m := &Manager{home: home, socketDir: socketDir, registry: reg, executable: executable, ctx: ctx, listener: listener, children: map[string]*child{}, generation: uuid.NewString()[:12], requests: make(chan struct{}, maxManagerRequests), rejections: make(chan struct{}, maxManagerRejections), ownerLock: startupLock, endpoint: endpoint}
	lockTransferred = true
	go m.serve()
	return m, nil
}

func lockManagerStartup(home string) (*os.File, error) {
	path := filepath.Join(home, ".agenthail", "claude-peers.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("peer manager is already running")
		}
		return nil, err
	}
	return file, nil
}

func unlockManagerStartup(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (m *Manager) serve() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		select {
		case m.requests <- struct{}{}:
		default:
			select {
			case m.rejections <- struct{}{}:
				go m.rejectBusy(conn)
			default:
				_ = json.NewEncoder(conn).Encode(managerResponse{Error: &managerError{Class: "unavailable", Error: "Claude peer manager admission is saturated; retry shortly"}})
				_ = conn.Close()
			}
			continue
		}
		go func() {
			defer func() { <-m.requests }()
			defer conn.Close()
			var request managerRequest
			if err := json.NewDecoder(io.LimitReader(conn, managerRequestBytes)).Decode(&request); err != nil {
				_ = json.NewEncoder(conn).Encode(managerResponse{Error: &managerError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: "invalid or oversized Claude peer manager request"}})
				return
			}
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()
			response := managerResponse{}
			switch request.Operation {
			case "send":
				response.Result, err = m.Send(ctx, request.SourceSessionID, request.TargetSocket, request.Message)
			case "ensure":
				err = m.Ensure(ctx, request.SourceSessionID)
			default:
				err = surface.DeliveryTerminal(fmt.Errorf("unsupported Claude peer manager operation %q", request.Operation), surface.DeliveryInvalidRequest)
			}
			if err != nil {
				response.Error = classifyManagerError(err)
			} else {
				response.OK = true
			}
			_ = json.NewEncoder(conn).Encode(response)
		}()
	}
}

func (m *Manager) rejectBusy(conn net.Conn) {
	defer func() { <-m.rejections }()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	var discarded managerRequest
	if err := json.NewDecoder(io.LimitReader(conn, managerRequestBytes)).Decode(&discarded); err != nil {
		_ = json.NewEncoder(conn).Encode(managerResponse{Error: &managerError{Class: "terminal", Kind: string(surface.DeliveryInvalidRequest), Error: "invalid or oversized Claude peer manager request"}})
		return
	}
	_ = json.NewEncoder(conn).Encode(managerResponse{Error: &managerError{Class: "unavailable", Error: "Claude peer manager is at its 32-request concurrency limit; retry shortly"}})
}

func (m *Manager) Ensure(ctx context.Context, id string) error {
	if id == "" {
		id = OperatorID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("peer manager is stopping")
	}
	if existing := m.children[id]; existing != nil {
		if existing.healthy() {
			existing.lastUsed = time.Now()
			return nil
		}
		existing.stop(2 * time.Second)
		existing.cleanup()
		delete(m.children, id)
	}
	if len(m.children) >= maxManagedPeers {
		return fmt.Errorf("Claude peer limit (%d) reached; wait for inactive peers to retire before retrying", maxManagedPeers)
	}
	var session *surface.Session
	var err error
	if id == OperatorID {
		session = &surface.Session{ID: id, Surface: "agenthail", Name: "operator", Status: surface.StatusIdle}
	} else {
		session, err = m.registry.Session(id)
		if err != nil {
			return fmt.Errorf("load sender %q: %w", id, err)
		}
		if session.Status == surface.StatusOffline {
			return fmt.Errorf("sender %q is offline", id)
		}
	}
	if id == OperatorID {
		if err := m.registry.RegisterSession(*session); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	processToken := uuid.NewString()
	command := exec.Command(m.executable, "claude-peer-worker", processToken)
	command.Env = append(os.Environ(), "AGENTHAIL_PEER_TOKEN="+processToken)
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	stderr := cappedBuffer{limit: 16 << 10}
	command.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	entry := &child{input: input, done: make(chan struct{}), process: command.Process, lastUsed: time.Now()}
	go func() { _ = command.Wait(); close(entry.done) }()
	fail := func(err error) error {
		input.Close()
		_ = command.Process.Kill()
		<-entry.done
		return err
	}
	startupFailure := func(err error) error {
		_ = fail(err)
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("start Claude peer: %s", detail)
		}
		return fmt.Errorf("start Claude peer: %w", err)
	}
	controlPath := claudepeer.WorkerControlPath(m.home, m.generation, command.Process.Pid)
	manifestPath := claudepeer.WorkerManifestPath(m.home, m.generation, command.Process.Pid)
	config := claudepeer.Config{Home: m.home, RegistryPath: m.registry.Path(), Session: *session, SocketDir: m.socketDir, ControlPath: controlPath, ManifestPath: manifestPath, Generation: m.generation, ProcessToken: processToken}
	if err := json.NewEncoder(input).Encode(config); err != nil {
		return fail(err)
	}
	ready := make(chan error, 1)
	go func() {
		var result struct {
			PID int `json:"pid"`
		}
		err := json.NewDecoder(io.LimitReader(output, workerReadyBytes)).Decode(&result)
		if err == nil && result.PID != command.Process.Pid {
			err = fmt.Errorf("peer worker readiness PID mismatch")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			return startupFailure(err)
		}
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	entry.paths = map[string]os.FileInfo{}
	socketDir := m.socketDir
	if socketDir == "" {
		socketDir = "/tmp/cc-socks"
	}
	recordPath := filepath.Join(m.home, ".claude", "sessions", fmt.Sprintf("%d.json", command.Process.Pid))
	for _, path := range []string{filepath.Join(socketDir, fmt.Sprintf("%d.sock", command.Process.Pid)), controlPath, manifestPath, recordPath} {
		if info, err := os.Lstat(path); err == nil {
			entry.paths[path] = info
		}
	}
	entry.controlPath = controlPath
	entry.recordPath = recordPath
	if data, err := os.ReadFile(recordPath); err == nil {
		_ = json.Unmarshal(data, &entry.record)
	}
	m.children[id] = entry
	return nil
}

func (m *Manager) Send(ctx context.Context, sourceID, targetSocket, message string) (*surface.SendResult, error) {
	if sourceID == "" {
		sourceID = OperatorID
	}
	if err := claudepeer.ValidateSend(sourceID, targetSocket, message); err != nil {
		return nil, err
	}
	if err := m.Ensure(ctx, sourceID); err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("register Claude peer: %w", err))
	}
	m.mu.Lock()
	entry := m.children[sourceID]
	if entry != nil {
		entry.lastUsed = time.Now()
	}
	m.mu.Unlock()
	if entry == nil || entry.controlPath == "" {
		return nil, surface.DeliveryUnavailable(errors.New("Claude peer registration disappeared before delivery"))
	}
	return claudepeer.Send(ctx, entry.controlPath, sourceID, targetSocket, message)
}

// RetireInactive bounds the set of Claude-visible Agenthail peers. Busy and
// recently active sessions remain discoverable; older sessions are recreated
// on demand by the next outbound send.
func (m *Manager) RetireInactive(now time.Time, maxIdle time.Duration) {
	m.mu.Lock()
	var retired []*child
	for id, entry := range m.children {
		if id == OperatorID {
			continue
		}
		select {
		case <-entry.done:
			delete(m.children, id)
			retired = append(retired, entry)
			continue
		default:
		}
		session, err := m.registry.Session(id)
		keep := now.Sub(entry.lastUsed) <= maxIdle || (err == nil && session.Status != surface.StatusOffline && (session.Status == surface.StatusBusy || (!session.LastActive.IsZero() && now.Sub(session.LastActive) <= maxIdle)))
		if keep {
			continue
		}
		delete(m.children, id)
		entry.input.Close()
		retired = append(retired, entry)
	}
	m.mu.Unlock()
	for _, entry := range retired {
		select {
		case <-entry.done:
		case <-time.After(2 * time.Second):
			_ = entry.process.Kill()
			<-entry.done
		}
		entry.cleanup()
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.listener.Close()
	children := m.children
	m.children = map[string]*child{}
	for _, child := range children {
		child.input.Close()
	}
	m.mu.Unlock()
	for _, child := range children {
		select {
		case <-child.done:
		case <-time.After(8 * time.Second):
			_ = child.process.Kill()
			<-child.done
		}
		child.cleanup()
	}
	path := managerPath(m.home)
	if current, err := os.Lstat(path); err == nil && m.endpoint != nil && os.SameFile(m.endpoint, current) {
		_ = os.Remove(path)
	}
	_ = os.Remove(filepath.Join(claudepeer.RuntimeRoot(m.home), m.generation))
	unlockManagerStartup(m.ownerLock)
	m.ownerLock = nil
}

func (c *child) healthy() bool {
	select {
	case <-c.done:
		return false
	default:
	}
	owned := c.paths[c.controlPath]
	if owned == nil {
		return false
	}
	current, err := os.Lstat(c.controlPath)
	if err != nil || current.Mode()&os.ModeSocket == 0 || !os.SameFile(owned, current) {
		return false
	}
	conn, err := net.DialTimeout("unix", c.controlPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *child) stop(timeout time.Duration) {
	_ = c.input.Close()
	select {
	case <-c.done:
	case <-time.After(timeout):
		_ = c.process.Kill()
		<-c.done
	}
}

func (c *child) cleanup() {
	for path, owned := range c.paths {
		current, err := os.Lstat(path)
		if err != nil {
			continue
		}
		matches := os.SameFile(owned, current)
		if path == c.recordPath && current.Mode().IsRegular() {
			data, _ := os.ReadFile(path)
			var record recordOwnership
			matches = c.record.Agenthail == "peer-worker" && json.Unmarshal(data, &record) == nil && record == c.record
		}
		if matches {
			_ = os.Remove(path)
		}
	}
}

func Ensure(ctx context.Context, home, sourceID string) error {
	response, err := requestManager(ctx, home, managerRequest{Operation: "ensure", SourceSessionID: sourceID})
	if err != nil {
		return err
	}
	if response.Error != nil {
		return surface.DeliveryUnavailable(fmt.Errorf("register Claude peer: %s", response.Error.Error))
	}
	return nil
}

func requestManager(ctx context.Context, home string, request managerRequest) (*managerResponse, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", managerPath(home))
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("Claude peer registration requires the daemon; start 'agenthail daemon start': %w", err))
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, managerRequestError(request, err)
	}
	var response managerResponse
	if err := json.NewDecoder(io.LimitReader(conn, managerReplyBytes)).Decode(&response); err != nil {
		return nil, managerRequestError(request, err)
	}
	return &response, nil
}

func managerRequestError(request managerRequest, err error) error {
	if request.Operation == "send" {
		return surface.DeliveryOutcomeUnknown(fmt.Errorf("Claude peer manager response was lost after request transmission: %w", err))
	}
	return surface.DeliveryUnavailable(err)
}

func Send(ctx context.Context, home, sourceID, socket, message string) (*surface.SendResult, error) {
	if sourceID == "" {
		sourceID = OperatorID
	}
	if err := claudepeer.ValidateSend(sourceID, socket, message); err != nil {
		return nil, err
	}
	response, err := requestManager(ctx, home, managerRequest{Operation: "send", SourceSessionID: sourceID, TargetSocket: socket, Message: message})
	if err != nil {
		return nil, err
	}
	if response.OK && response.Result != nil {
		return response.Result, nil
	}
	if response.Error == nil {
		return nil, surface.DeliveryOutcomeUnknown(errors.New("malformed Claude peer manager response"))
	}
	switch response.Error.Class {
	case "terminal":
		return nil, surface.DeliveryTerminal(errors.New(response.Error.Error), surface.DeliveryTerminalKind(response.Error.Kind))
	case "unavailable":
		return nil, surface.DeliveryUnavailable(errors.New(response.Error.Error))
	default:
		return nil, surface.DeliveryOutcomeUnknown(errors.New(response.Error.Error))
	}
}

func classifyManagerError(err error) *managerError {
	if surface.IsDeliveryTerminal(err) {
		return &managerError{Class: "terminal", Kind: string(surface.DeliveryTerminalReason(err)), Error: err.Error()}
	}
	if surface.IsDeliveryOutcomeUnknown(err) {
		return &managerError{Class: "unknown", Error: err.Error()}
	}
	return &managerError{Class: "unavailable", Error: err.Error()}
}
