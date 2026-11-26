package acphost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/surface"
)

const outcomeRetention = time.Hour

type outcome struct {
	value Outcome
	at    time.Time
	done  chan struct{}
}

type Host struct {
	home    string
	version string
	store   Store
	agents  []Agent
	log     *log.Logger

	mu       sync.Mutex
	opening  map[string]*sync.Mutex
	sessions map[string]*session
	outcomes map[string]*outcome
	watchers map[chan Event]struct{}
	draining bool
	listener net.Listener
	socket   string
	exited   chan struct{}
	exitOnce sync.Once
}

func NewHost(home, version string, agents []Agent, logger *log.Logger) *Host {
	return &Host{home: home, version: version, store: DefaultStore(home), agents: agents, log: logger,
		sessions: map[string]*session{}, outcomes: map[string]*outcome{}, watchers: map[chan Event]struct{}{}, exited: make(chan struct{})}
}

func (h *Host) logf(format string, args ...any) {
	if h.log != nil {
		h.log.Printf(format, args...)
	}
}

func (h *Host) Serve(listener net.Listener, socket string) error {
	h.mu.Lock()
	h.listener, h.socket = listener, socket
	h.mu.Unlock()
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-h.exited:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go h.handle(conn)
	}
}

func (h *Host) Exited() <-chan struct{} { return h.exited }

func (h *Host) exit() {
	h.exitOnce.Do(func() {
		h.mu.Lock()
		listener, socket := h.listener, h.socket
		h.mu.Unlock()
		if listener != nil {
			listener.Close()
		}
		if socket != "" {
			os.Remove(socket)
		}
		close(h.exited)
	})
}

func (h *Host) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 64<<10)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var call Call
	if err := json.Unmarshal(line, &call); err != nil {
		writeReply(conn, nil, callError(CodeInvalid, "malformed call"))
		return
	}
	if call.Method == "subscribe" {
		h.subscribe(conn)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()
	result, err := h.dispatch(ctx, call)
	writeReply(conn, result, err)
}

func writeReply(conn net.Conn, result any, err error) {
	reply := Reply{}
	if err != nil {
		var typed *CallError
		if !errors.As(err, &typed) {
			typed = callError(CodeFailed, "%v", err)
		}
		reply.Error = typed
	} else if result != nil {
		reply.Result, _ = json.Marshal(result)
	}
	data, _ := json.Marshal(reply)
	_, _ = conn.Write(append(data, '\n'))
}

func (h *Host) subscribe(conn net.Conn) {
	events := make(chan Event, 256)
	h.mu.Lock()
	h.watchers[events] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.watchers, events)
		h.mu.Unlock()
	}()
	gone := make(chan struct{})
	go func() {
		buffer := make([]byte, 1)
		_, _ = conn.Read(buffer)
		close(gone)
	}()
	encoder := json.NewEncoder(conn)
	if encoder.Encode(Event{Status: "subscribed"}) != nil {
		return
	}
	for {
		select {
		case event := <-events:
			if encoder.Encode(event) != nil {
				return
			}
		case <-gone:
			return
		case <-h.exited:
			return
		}
	}
}

func (h *Host) publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for watcher := range h.watchers {
		select {
		case watcher <- event:
		default:
		}
	}
}

func (h *Host) dispatch(ctx context.Context, call Call) (any, error) {
	switch call.Method {
	case "hello":
		h.mu.Lock()
		defer h.mu.Unlock()
		return Hello{Protocol: Protocol, Version: h.version, PID: os.Getpid(), Draining: h.draining}, nil
	case "held":
		return h.held(), nil
	case "outcome":
		var p OutcomeParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		return h.lookupOutcome(ctx, p.Key), nil
	case "drain":
		h.drain()
		return struct{}{}, nil
	}
	return h.keyed(call.Key, func() (any, error) { return h.act(ctx, call) })
}

func (h *Host) keyed(key string, act func() (any, error)) (any, error) {
	if key == "" {
		return act()
	}
	h.mu.Lock()
	h.pruneOutcomesLocked()
	if existing := h.outcomes[key]; existing != nil {
		h.mu.Unlock()
		<-existing.done
		if existing.value.Error != nil {
			return nil, existing.value.Error
		}
		return existing.value.Result, nil
	}
	entry := &outcome{value: Outcome{State: OutcomeStarted}, at: time.Now(), done: make(chan struct{})}
	h.outcomes[key] = entry
	h.mu.Unlock()
	result, err := act()
	h.mu.Lock()
	if err != nil {
		var typed *CallError
		if !errors.As(err, &typed) {
			typed = callError(CodeFailed, "%v", err)
		}
		entry.value = Outcome{State: OutcomeFailed, Error: typed}
	} else {
		raw, _ := json.Marshal(result)
		entry.value = Outcome{State: OutcomeDone, Result: raw}
	}
	close(entry.done)
	h.mu.Unlock()
	return result, err
}

func (h *Host) lookupOutcome(ctx context.Context, key string) Outcome {
	h.mu.Lock()
	entry := h.outcomes[key]
	if entry == nil {
		entry = &outcome{value: Outcome{State: OutcomeFailed, Error: callError(CodeNotFound, "the host never received this request")}, at: time.Now(), done: make(chan struct{})}
		close(entry.done)
		h.outcomes[key] = entry
	}
	h.mu.Unlock()
	select {
	case <-entry.done:
	case <-ctx.Done():
		return Outcome{State: OutcomeStarted}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return entry.value
}

func (h *Host) pruneOutcomesLocked() {
	for key, entry := range h.outcomes {
		select {
		case <-entry.done:
			if time.Since(entry.at) > outcomeRetention {
				delete(h.outcomes, key)
			}
		default:
		}
	}
}

func (h *Host) act(ctx context.Context, call Call) (any, error) {
	switch call.Method {
	case "create":
		var p CreateParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		return h.create(ctx, p)
	case "send":
		var p SendParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		s, err := h.open(p.SessionID)
		if err != nil {
			return nil, err
		}
		return s.send(ctx, p)
	case "cancel":
		var p SessionParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		s := h.heldSession(p.SessionID)
		if s == nil {
			return struct{}{}, nil
		}
		return struct{}{}, s.cancel()
	case "respond":
		var p RespondParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		s := h.heldSession(p.SessionID)
		if s == nil {
			return nil, callError(CodeStale, "this request was already answered or has ended")
		}
		return s.respond(p.RequestID, p.Choice)
	case "setOption":
		var p OptionParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		s, err := h.open(p.SessionID)
		if err != nil {
			return nil, err
		}
		s.startMu.Lock()
		defer s.startMu.Unlock()
		if err := s.closedError(); err != nil {
			return nil, err
		}
		preferred := ""
		if p.Category == "mode" {
			preferred = p.Value
		}
		if err := s.ensureRunning(ctx, nil, preferred); err != nil {
			return nil, err
		}
		if err := s.setOption(ctx, p.Category, p.Value); err != nil {
			return nil, err
		}
		if p.Category == "mode" {
			s.mu.Lock()
			s.state.Refused = ""
			s.saveLocked()
			s.mu.Unlock()
		}
		s.changed()
		return s.snapshot(), nil
	case "close":
		var p SessionParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, callError(CodeInvalid, "%v", err)
		}
		h.closeSession(p.SessionID, true, false)
		return struct{}{}, nil
	}
	return nil, callError(CodeInvalid, "unknown method %q", call.Method)
}

func (h *Host) agent(id string) (Agent, bool) {
	for _, agent := range h.agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return Agent{}, false
}

func (h *Host) heldSession(id string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

func (h *Host) held() HeldResult {
	h.mu.Lock()
	sessions := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	result := HeldResult{Sessions: []State{}}
	for _, s := range sessions {
		result.Sessions = append(result.Sessions, s.snapshot())
	}
	return result
}

func (h *Host) create(ctx context.Context, p CreateParams) (State, error) {
	agent, ok := h.agent(p.Agent)
	if !ok {
		return State{}, callError(CodeInvalid, "no ACP agent %q is configured", p.Agent)
	}
	if p.SessionID == "" || filepath.Base(p.SessionID) != p.SessionID {
		return State{}, callError(CodeInvalid, "a session id is required")
	}
	if p.Ceiling != "" && RankIndex(p.Ceiling) < 0 {
		return State{}, callError(CodeInvalid, "unknown ceiling %q", p.Ceiling)
	}
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return State{}, callError(CodeDraining, "this ACP host is being replaced")
	}
	if h.sessions[p.SessionID] != nil {
		h.mu.Unlock()
		return State{}, callError(CodeInvalid, "session %s already exists", p.SessionID)
	}
	h.mu.Unlock()
	lock, err := h.store.lock(agent.ID, p.SessionID)
	if err != nil {
		return State{}, err
	}
	s := &session{host: h, agent: agent, lock: lock, pending: map[string]*pendingRequest{},
		state: State{SessionID: p.SessionID, Agent: agent.ID, Cwd: p.Cwd, Ceiling: p.Ceiling, CreatedAt: time.Now().UTC(), Status: StatusStopped}}
	h.mu.Lock()
	h.sessions[p.SessionID] = s
	h.mu.Unlock()
	if err := s.create(ctx, p); err != nil {
		h.mu.Lock()
		delete(h.sessions, p.SessionID)
		h.mu.Unlock()
		lock.release()
		return State{}, err
	}
	s.changed()
	return s.snapshot(), nil
}

func (h *Host) open(id string) (*session, error) {
	h.mu.Lock()
	if h.opening == nil {
		h.opening = map[string]*sync.Mutex{}
	}
	gate := h.opening[id]
	if gate == nil {
		gate = &sync.Mutex{}
		h.opening[id] = gate
	}
	h.mu.Unlock()
	gate.Lock()
	defer gate.Unlock()
	h.mu.Lock()
	if s := h.sessions[id]; s != nil {
		h.mu.Unlock()
		return s, nil
	}
	draining := h.draining
	h.mu.Unlock()
	if draining {
		return nil, callError(CodeDraining, "this ACP host is being replaced")
	}
	var state State
	var agent Agent
	found := false
	for _, candidate := range h.agents {
		if record, err := h.store.ReadRecord(candidate.ID, id); err == nil {
			state, agent, found = record, candidate, true
			break
		}
	}
	if !found {
		return nil, callError(CodeNotFound, "no ACP session %s", id)
	}
	lock, err := h.store.lockWaiting(agent.ID, id, handoverWait)
	if err != nil {
		return nil, err
	}
	if state, err = h.store.ReadRecord(agent.ID, id); err != nil {
		lock.release()
		return nil, err
	}
	writer, err := h.store.openTranscript(agent.ID, id)
	if err != nil {
		lock.release()
		if errors.Is(err, ErrTranscriptGone) {
			return nil, callError(CodeInvalid, "%v; the conversation is still in %s, and a new session can carry on from it", err, agent.DisplayName())
		}
		return nil, err
	}
	stopLeftover(state)
	s := &session{host: h, agent: agent, lock: lock, writer: writer, norm: newNormalizer(writer), pending: map[string]*pendingRequest{}, state: state}
	if state.TurnID != "" {
		s.norm.turnID = state.TurnID
		s.norm.endTurn("", "The agent stopped during the turn.")
		s.state.LastTurnID, s.state.LastStop, s.state.TurnID = state.TurnID, "failed", ""
	}
	s.state.Status, s.state.PID, s.state.PIDStart = StatusIdle, 0, ""
	s.saveLocked()
	h.mu.Lock()
	if existing := h.sessions[id]; existing != nil {
		h.mu.Unlock()
		writer.close()
		lock.release()
		return existing, nil
	}
	h.sessions[id] = s
	h.mu.Unlock()
	return s, nil
}

func stopLeftover(state State) {
	if state.PID <= 0 || state.PIDStart == "" {
		return
	}
	started, err := procstart.Lstart(context.Background(), state.PID)
	if err != nil || started != state.PIDStart {
		return
	}
	_ = syscall.Kill(-state.PID, syscall.SIGKILL)
}

func (h *Host) closeSession(id string, closeAgent, onlyQuiet bool) bool {
	h.mu.Lock()
	s := h.sessions[id]
	h.mu.Unlock()
	if s != nil {
		if !s.close(closeAgent, onlyQuiet) {
			return false
		}
		h.publish(Event{SessionID: id, Status: StatusStopped})
	}
	h.mu.Lock()
	if h.sessions[id] == s {
		delete(h.sessions, id)
	}
	draining, remaining := h.draining, len(h.sessions)
	h.mu.Unlock()
	if draining && remaining == 0 {
		h.exit()
	}
	return true
}

func (h *Host) drain() {
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		return
	}
	h.draining = true
	listener, socket := h.listener, h.socket
	h.mu.Unlock()
	if socket != "" && listener != nil {
		private := fmt.Sprintf("%s.%d.sock", socket[:len(socket)-len(".sock")], os.Getpid())
		if next, err := net.Listen("unix", private); err == nil {
			_ = os.Chmod(private, 0o600)
			os.Remove(socket)
			listener.Close()
			h.mu.Lock()
			h.listener, h.socket = next, private
			h.mu.Unlock()
			go func() { _ = h.Serve(next, private) }()
		}
	}
	releaseHostLock()
	go h.drainSessions()
}

func (h *Host) drainSessions() {
	for {
		h.mu.Lock()
		var quiet []string
		for id, s := range h.sessions {
			state := s.snapshot()
			if state.Status != StatusBusy && len(state.Requests) == 0 {
				quiet = append(quiet, id)
			}
		}
		remaining := len(h.sessions)
		h.mu.Unlock()
		if remaining == 0 {
			h.exit()
			return
		}
		for _, id := range quiet {
			h.closeSession(id, false, true)
		}
		select {
		case <-h.exited:
			return
		case <-time.After(time.Second):
		}
	}
}

var hostLock *os.File

func releaseHostLock() {
	if hostLock != nil {
		_ = syscall.Flock(int(hostLock.Fd()), syscall.LOCK_UN)
		hostLock.Close()
		hostLock = nil
	}
}

func TranscriptIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil {
		return "", err
	}
	var first Line
	if err := json.Unmarshal(line, &first); err != nil || first.Header == nil {
		return "", fmt.Errorf("%s does not start with its header: %w", path, surface.ErrTranscriptUnavailable)
	}
	return first.Header.Identity, nil
}
