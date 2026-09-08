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
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const OperatorID = "agenthail-operator"

type child struct {
	input   io.WriteCloser
	done    chan struct{}
	process *os.Process
	paths   map[string]os.FileInfo
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
}

func managerPath(home string) string {
	return filepath.Join(home, ".agenthail", "claude-peers.sock")
}

func Start(ctx context.Context, home string, reg *registry.Registry, executable string) (*Manager, error) {
	path := managerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// The daemon's process lock serializes ownership of this private endpoint.
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("peer manager path is not a socket: %s", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("peer manager is already running")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	m := &Manager{home: home, registry: reg, executable: executable, ctx: ctx, listener: listener, children: map[string]*child{}}
	go m.serve()
	return m, nil
}

func (m *Manager) serve() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
			var request struct {
				SourceSessionID string `json:"sourceSessionId"`
			}
			if err := json.NewDecoder(io.LimitReader(conn, 8192)).Decode(&request); err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()
			err := m.Ensure(ctx, request.SourceSessionID)
			response := struct {
				Error string `json:"error,omitempty"`
			}{}
			if err != nil {
				response.Error = err.Error()
			}
			_ = json.NewEncoder(conn).Encode(response)
		}()
	}
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
		select {
		case <-existing.done:
			existing.cleanup()
			delete(m.children, id)
		default:
			return nil
		}
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
	command := exec.Command(m.executable, "claude-peer-worker")
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	entry := &child{input: input, done: make(chan struct{}), process: command.Process}
	go func() { _ = command.Wait(); close(entry.done) }()
	fail := func(err error) error {
		input.Close()
		_ = command.Process.Kill()
		<-entry.done
		return err
	}
	config := claudepeer.Config{Home: m.home, RegistryPath: m.registry.Path(), Session: *session, SocketDir: m.socketDir}
	if err := json.NewEncoder(input).Encode(config); err != nil {
		return fail(err)
	}
	ready := make(chan error, 1)
	go func() {
		var result struct {
			PID int `json:"pid"`
		}
		err := json.NewDecoder(io.LimitReader(output, 8192)).Decode(&result)
		if err == nil && result.PID != command.Process.Pid {
			err = fmt.Errorf("peer worker readiness PID mismatch")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			return fail(fmt.Errorf("start Claude peer: %w", err))
		}
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	entry.paths = map[string]os.FileInfo{}
	socketDir := m.socketDir
	if socketDir == "" {
		socketDir = "/tmp/cc-socks"
	}
	for _, path := range []string{filepath.Join(socketDir, fmt.Sprintf("%d.sock", command.Process.Pid)), claudepeer.ControlPath(m.home, id), filepath.Join(m.home, ".claude", "sessions", fmt.Sprintf("%d.json", command.Process.Pid))} {
		if info, err := os.Lstat(path); err == nil {
			entry.paths[path] = info
		}
	}
	m.children[id] = entry
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
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
}

func (c *child) cleanup() {
	for path, owned := range c.paths {
		current, err := os.Lstat(path)
		if err != nil {
			continue
		}
		matches := os.SameFile(owned, current)
		if current.Mode().IsRegular() && filepath.Ext(path) == ".json" {
			data, _ := os.ReadFile(path)
			var record struct {
				PID       int    `json:"pid"`
				Agenthail string `json:"agenthail"`
			}
			matches = json.Unmarshal(data, &record) == nil && record.PID == c.process.Pid && record.Agenthail == "peer-worker"
		}
		if matches {
			_ = os.Remove(path)
		}
	}
}

func Ensure(ctx context.Context, home, sourceID string) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", managerPath(home))
	if err != nil {
		return surface.DeliveryUnavailable(fmt.Errorf("Claude peer registration requires the daemon; start 'agenthail daemon start': %w", err))
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(map[string]string{"sourceSessionId": sourceID}); err != nil {
		return surface.DeliveryUnavailable(err)
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(conn, 8192)).Decode(&response); err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if response.Error != "" {
		return surface.DeliveryUnavailable(fmt.Errorf("register Claude peer: %s", response.Error))
	}
	return nil
}

func Send(ctx context.Context, home, sourceID, socket, message string) (*surface.SendResult, error) {
	if sourceID == "" {
		sourceID = OperatorID
	}
	if err := Ensure(ctx, home, sourceID); err != nil {
		return nil, err
	}
	return claudepeer.Send(ctx, home, sourceID, socket, message)
}
