package acphost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/acp"
	"github.com/zm2231/agenthail/internal/procstart"
	"github.com/zm2231/agenthail/internal/surface"
)

var (
	idleStop      = 15 * time.Minute
	agentTimeout  = 2 * time.Minute
	stopGrace     = 5 * time.Second
	handoverWait  = 3 * stopGrace
	errNotRunning = errors.New("the agent is not running")
)

type permissionAnswer struct {
	result  acp.RequestPermissionResult
	written chan error
}

type pendingRequest struct {
	request Request
	answer  chan permissionAnswer
}

type session struct {
	host  *Host
	agent Agent
	lock  *sessionLock

	startMu sync.Mutex
	mu      sync.Mutex
	state   State
	writer  *transcriptWriter
	norm    *normalizer
	cmd     *exec.Cmd
	conn    *acp.Conn
	caps    acp.AgentCapabilities
	loading bool
	pending map[string]*pendingRequest
	idle    *time.Timer
	exited  chan struct{}
	closed  bool
}

func (s *session) snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *session) snapshotLocked() State {
	state := s.state
	state.Requests = make([]Request, 0, len(s.pending))
	for _, pending := range s.pending {
		state.Requests = append(state.Requests, pending.request)
	}
	slices.SortFunc(state.Requests, func(a, b Request) int { return a.CreatedAt.Compare(b.CreatedAt) })
	state.Configs()
	return state
}

func (s *State) Configs() {
	for _, option := range s.ConfigOptions {
		switch option.Category {
		case "mode":
			s.Mode = option.CurrentValue
		case "model":
			s.Model = option.CurrentValue
		}
	}
}

func (s *session) saveLocked() {
	if err := s.host.store.WriteRecord(s.state); err != nil {
		s.host.logf("save session %s: %v", s.state.SessionID, err)
	}
}

func (s *session) changed() {
	s.host.publish(Event{SessionID: s.state.SessionID, Status: s.snapshot().Status})
}

func (s *session) Request(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != "session/request_permission" {
		return nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: "Agenthail serves no " + method}
	}
	var p acp.RequestPermissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	request := Request{ToolCallID: p.ToolCall.ToolCallID}
	if s.norm != nil {
		request, _ = s.norm.request(p.ToolCall.ToolCallID)
	}
	if p.ToolCall.Title != nil && request.Title == "" {
		request.Title = *p.ToolCall.Title
	}
	if p.ToolCall.Kind != "" && request.Kind == "" {
		request.Kind = p.ToolCall.Kind
	}
	if len(p.ToolCall.RawInput) > 0 && len(request.RawInput) == 0 {
		request.RawInput = p.ToolCall.RawInput
	}
	request.ID, request.Options, request.CreatedAt = "req-"+uuid.NewString(), p.Options, time.Now().UTC()
	pending := &pendingRequest{request: request, answer: make(chan permissionAnswer, 1)}
	s.pending[request.ID] = pending
	s.mu.Unlock()
	s.changed()
	select {
	case answer := <-pending.answer:
		return acp.Acknowledged{Result: answer.result, Written: func(err error) { answer.written <- err }}, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.mu.Unlock()
		s.changed()
		return acp.Cancelled(), nil
	}
}

func (s *session) Notification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	var u acp.Update
	if json.Unmarshal(n.Update, &u) != nil {
		return
	}
	s.mu.Lock()
	guard := s.applyStateLocked(u)
	if !s.loading && s.norm != nil && s.norm.turnID != "" {
		s.norm.update(u)
	}
	loading := s.loading
	s.mu.Unlock()
	if guard != "" && !loading {
		go s.enforceCeiling(guard)
	}
}

func (s *session) applyStateLocked(u acp.Update) string {
	before := s.state.Mode
	switch u.SessionUpdate {
	case "current_mode_update":
		s.state.Mode = u.CurrentModeID
		s.state.ConfigOptions = slices.Clone(s.state.ConfigOptions)
		for i := range s.state.ConfigOptions {
			if s.state.ConfigOptions[i].Category == "mode" {
				s.state.ConfigOptions[i].CurrentValue = u.CurrentModeID
			}
		}
	case "config_option_update":
		if len(u.ConfigOptions) > 0 {
			s.state.ConfigOptions = u.ConfigOptions
			s.state.Configs()
		}
	case "session_info_update":
		if u.Title != nil {
			s.state.Title = *u.Title
		}
	case "usage_update":
		if u.Used != nil {
			s.state.ContextUsed = *u.Used
		}
		if u.Size != nil {
			s.state.ContextSize = *u.Size
		}
	case "available_commands_update":
		s.state.Commands = u.Commands
	default:
		return ""
	}
	s.saveLocked()
	if s.state.Mode != before && s.aboveCeilingLocked(s.state.Mode) {
		return before
	}
	return ""
}

func (s *session) withinCeiling(mode string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.aboveCeilingLocked(mode)
}

func (s *session) aboveCeilingLocked(mode string) bool {
	return s.state.Ceiling != "" && mode != "" && RankIndex(s.agent.ModeRank(mode)) > RankIndex(s.state.Ceiling)
}

func (s *session) enforceCeiling(previous string) {
	s.mu.Lock()
	current, agentSession, conn := s.state.Mode, s.state.AgentSessionID, s.conn
	s.cancelRequestsLocked()
	if s.norm != nil && s.norm.turnID != "" {
		s.norm.noticeItem(fmt.Sprintf("The agent switched itself to the %s mode, above this session's ceiling of %s. Agenthail stopped the turn and set the mode back to %s.", current, s.state.Ceiling, previous))
	}
	s.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.Notify("session/cancel", acp.SessionIDParams{SessionID: agentSession})
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()
	if err := s.setModeOnAgent(ctx, previous); err != nil {
		s.mu.Lock()
		s.state.Refused = fmt.Sprintf("The agent switched itself to the %s mode, above this session's ceiling of %s, and Agenthail could not set it back (%v). Change the session's mode within the ceiling to send to it again.", current, s.state.Ceiling, err)
		s.saveLocked()
		s.mu.Unlock()
		s.stopProcess()
	}
	s.changed()
}

func (s *session) cancelRequestsLocked() {
	for id, pending := range s.pending {
		pending.answer <- permissionAnswer{result: acp.Cancelled(), written: make(chan error, 1)}
		delete(s.pending, id)
	}
}

func (s *session) ensureRunning(ctx context.Context, mcp []acp.MCPServer) error {
	s.mu.Lock()
	running := s.conn != nil
	agentSession, recorded := s.state.AgentSessionID, s.state.Mode
	if s.aboveCeilingLocked(recorded) {
		recorded = ""
	}
	s.mu.Unlock()
	if running {
		return nil
	}
	if err := s.startProcess(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.loading = true
	conn := s.conn
	s.mu.Unlock()
	var setup acp.SessionSetup
	err := conn.Request(ctx, "session/load", acp.LoadSessionParams{SessionID: agentSession, Cwd: s.state.Cwd, MCPServers: mcpOrEmpty(mcp)}, &setup)
	s.mu.Lock()
	s.loading = false
	if err == nil {
		s.applySetupLocked(setup)
		s.saveLocked()
	}
	loaded, above := s.state.Mode, s.aboveCeilingLocked(s.state.Mode)
	s.mu.Unlock()
	if err != nil {
		s.stopProcess()
		return fmt.Errorf("load the %s session: %w", s.agent.DisplayName(), err)
	}
	if above {
		target := recorded
		if target == "" {
			target, err = s.pickModeWithinCeiling()
		}
		if err == nil {
			err = s.setModeOnAgent(ctx, target)
		}
		if mode := s.snapshot().Mode; err == nil && s.withinCeiling(mode) {
			return nil
		}
		s.stopProcess()
		return callError("exceeds_ceiling", "%s reopened the session in %s, above its ceiling of %s, and Agenthail could not set a mode within it: %v", s.agent.DisplayName(), loaded, s.state.Ceiling, err)
	}
	return nil
}

func mcpOrEmpty(servers []acp.MCPServer) []acp.MCPServer {
	if servers == nil {
		return []acp.MCPServer{}
	}
	return servers
}

func (s *session) applySetupLocked(setup acp.SessionSetup) {
	if setup.Modes != nil {
		s.state.Modes = setup.Modes.AvailableModes
		s.state.Mode = setup.Modes.CurrentModeID
	}
	if len(setup.ConfigOptions) > 0 {
		s.state.ConfigOptions = setup.ConfigOptions
	}
	s.state.Configs()
}

func (s *session) startProcess(ctx context.Context) error {
	argv := s.agent.Command
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s is not installed here: %w", s.agent.DisplayName(), err)
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Dir = s.state.Cwd
	cmd.Env = surface.AgentEnv(os.Environ())
	for name, value := range s.agent.Env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.Env = append(cmd.Env, "AGENTHAIL_SESSION_ID="+s.state.SessionID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if log, err := os.OpenFile(s.host.store.TranscriptPath(s.agent.ID, s.state.SessionID)+".log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		cmd.Stderr = log
		defer log.Close()
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.agent.DisplayName(), err)
	}
	conn := acp.NewConn(stdout, stdin, s)
	exited := make(chan struct{})
	s.mu.Lock()
	s.cmd, s.conn, s.exited = cmd, conn, exited
	s.state.PID = cmd.Process.Pid
	if started, err := procstart.Lstart(context.Background(), cmd.Process.Pid); err == nil {
		s.state.PIDStart = started
	}
	s.saveLocked()
	s.mu.Unlock()
	conn.Start()
	go s.watch(cmd, conn, exited)
	var init acp.InitializeResult
	err = conn.Request(ctx, "initialize", acp.InitializeParams{ProtocolVersion: acp.ProtocolVersion, ClientCapabilities: acp.ClientCapabilities{}, ClientInfo: acp.ClientInfo{Name: "agenthail", Version: s.host.version}}, &init)
	if err != nil {
		s.stopProcess()
		var agentErr *acp.Error
		if errors.As(err, &agentErr) && agentErr.Code == acp.CodeAuthRequired {
			return fmt.Errorf("%s is signed out; sign in to it on this machine", s.agent.DisplayName())
		}
		return fmt.Errorf("initialize %s: %w", s.agent.DisplayName(), err)
	}
	s.mu.Lock()
	s.caps = init.AgentCapabilities
	s.mu.Unlock()
	return nil
}

func (s *session) watch(cmd *exec.Cmd, conn *acp.Conn, exited chan struct{}) {
	defer close(exited)
	<-conn.Done()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	s.mu.Lock()
	if s.conn == conn {
		s.conn, s.cmd = nil, nil
		s.cancelRequestsLocked()
		if s.norm != nil && s.norm.turnID != "" {
			s.norm.endTurn("", "The agent stopped during the turn.")
			s.finishTurnLocked("failed")
		}
		s.state.PID, s.state.PIDStart = 0, ""
		if s.state.Status != StatusBusy {
			s.state.Status = StatusIdle
		}
		s.saveLocked()
	}
	s.mu.Unlock()
	s.changed()
}

func (s *session) stopProcess() {
	s.mu.Lock()
	cmd, exited := s.cmd, s.exited
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if closer, ok := cmd.Stdin.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}
}

func (s *session) create(ctx context.Context, params CreateParams) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.startProcess(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("%s exited as it started", s.agent.DisplayName())
	}
	var setup acp.SessionSetup
	if err := conn.Request(ctx, "session/new", acp.NewSessionParams{Cwd: params.Cwd, MCPServers: mcpOrEmpty(params.MCPServers)}, &setup); err != nil {
		s.stopProcess()
		return fmt.Errorf("start a %s session: %w", s.agent.DisplayName(), err)
	}
	s.mu.Lock()
	s.state.AgentSessionID = setup.SessionID
	s.applySetupLocked(setup)
	s.mu.Unlock()
	fail := func(err error) error {
		_ = conn.Request(ctx, "session/close", acp.SessionIDParams{SessionID: setup.SessionID}, nil)
		s.stopProcess()
		return err
	}
	mode := params.Mode
	if mode == "" && s.state.Ceiling != "" {
		picked, err := s.pickModeWithinCeiling()
		if err != nil {
			return fail(err)
		}
		mode = picked
	}
	if mode != "" && mode != s.state.Mode {
		if err := s.setOption(ctx, "mode", mode); err != nil {
			return fail(err)
		}
	}
	s.mu.Lock()
	started, above := s.state.Mode, s.aboveCeilingLocked(s.state.Mode)
	s.mu.Unlock()
	if above {
		return fail(callError("exceeds_ceiling", "Your ceiling is %s, and %s would start in %s, above it.", s.state.Ceiling, s.agent.DisplayName(), started))
	}
	if params.Model != "" && params.Model != s.state.Model {
		if err := s.setOption(ctx, "model", params.Model); err != nil {
			return fail(err)
		}
	}
	writer, err := s.host.store.createTranscript(s.agent.ID, s.state.SessionID)
	if err != nil {
		return fail(fmt.Errorf("create the session's transcript: %w", err))
	}
	s.mu.Lock()
	s.writer, s.norm = writer, newNormalizer(writer)
	s.state.Status, s.state.LastActive = StatusIdle, time.Now().UTC()
	s.saveLocked()
	s.mu.Unlock()
	return nil
}

func (s *session) pickModeWithinCeiling() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Mode
	if current != "" && !s.aboveCeilingLocked(current) {
		return current, nil
	}
	best, bestRank := "", -1
	for _, mode := range s.modeIDsLocked() {
		rank := RankIndex(s.agent.ModeRank(mode))
		if !s.aboveCeilingLocked(mode) && rank > bestRank {
			best, bestRank = mode, rank
		}
	}
	if best == "" {
		return "", callError("exceeds_ceiling", "Your ceiling is %s, and %s has no mode that low.", s.state.Ceiling, s.agent.DisplayName())
	}
	return best, nil
}

func (s *session) modeIDsLocked() []string {
	var ids []string
	for _, option := range s.state.ConfigOptions {
		if option.Category == "mode" {
			for _, value := range option.Options {
				ids = append(ids, value.Value)
			}
		}
	}
	if len(ids) == 0 {
		for _, mode := range s.state.Modes {
			ids = append(ids, mode.ID)
		}
	}
	return ids
}

func (s *session) setOption(ctx context.Context, category, value string) error {
	s.mu.Lock()
	if category == "mode" && s.aboveCeilingLocked(value) {
		s.mu.Unlock()
		return callError("exceeds_ceiling", "%s is above this session's ceiling of %s.", value, s.state.Ceiling)
	}
	var option *acp.ConfigOption
	for i := range s.state.ConfigOptions {
		if s.state.ConfigOptions[i].Category == category {
			option = &s.state.ConfigOptions[i]
		}
	}
	listed := false
	if option != nil {
		listed = slices.ContainsFunc(option.Options, func(v acp.ConfigValue) bool { return v.Value == value })
	} else if category == "mode" {
		listed = slices.Contains(s.modeIDsLocked(), value)
	}
	conn, agentSession := s.conn, s.state.AgentSessionID
	s.mu.Unlock()
	if !listed {
		return callError(CodeInvalid, "%s lists no %s %q", s.agent.DisplayName(), category, value)
	}
	if conn == nil {
		return errNotRunning
	}
	if option != nil {
		var result acp.SetConfigOptionResult
		if err := conn.Request(ctx, "session/set_config_option", acp.SetConfigOptionParams{SessionID: agentSession, ConfigID: option.ID, Value: value}, &result); err != nil {
			return err
		}
		s.mu.Lock()
		if len(result.ConfigOptions) > 0 {
			s.state.ConfigOptions = result.ConfigOptions
		}
		s.state.Configs()
		s.saveLocked()
		s.mu.Unlock()
		return nil
	}
	if category == "mode" {
		return s.setModeOnAgent(ctx, value)
	}
	if err := conn.Request(ctx, "session/set_model", acp.SetModelParams{SessionID: agentSession, ModelID: value}, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Model = value
	s.saveLocked()
	s.mu.Unlock()
	return nil
}

func (s *session) setModeOnAgent(ctx context.Context, mode string) error {
	s.mu.Lock()
	conn, agentSession := s.conn, s.state.AgentSessionID
	var optionID string
	for _, option := range s.state.ConfigOptions {
		if option.Category == "mode" {
			optionID = option.ID
		}
	}
	s.mu.Unlock()
	if conn == nil {
		return errNotRunning
	}
	if optionID != "" {
		var result acp.SetConfigOptionResult
		if err := conn.Request(ctx, "session/set_config_option", acp.SetConfigOptionParams{SessionID: agentSession, ConfigID: optionID, Value: mode}, &result); err != nil {
			return err
		}
		s.mu.Lock()
		if len(result.ConfigOptions) > 0 {
			s.state.ConfigOptions = result.ConfigOptions
		}
		s.state.Configs()
		s.saveLocked()
		s.mu.Unlock()
		return nil
	}
	if err := conn.Request(ctx, "session/set_mode", acp.SetModeParams{SessionID: agentSession, ModeID: mode}, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Mode = mode
	s.saveLocked()
	s.mu.Unlock()
	return nil
}

func (s *session) send(ctx context.Context, params SendParams) (SendResult, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.closedError(); err != nil {
		return SendResult{}, err
	}
	s.mu.Lock()
	refused, busy := s.state.Refused, s.state.Status == StatusBusy
	s.mu.Unlock()
	if refused != "" {
		return SendResult{}, callError(CodeInvalid, "%s", refused)
	}
	if busy {
		return SendResult{}, callError(CodeBusy, "the session is running a turn")
	}
	if err := s.ensureRunning(ctx, params.MCPServers); err != nil {
		return SendResult{}, err
	}
	turnID := uuid.NewString()
	s.mu.Lock()
	if s.state.Status == StatusBusy {
		s.mu.Unlock()
		return SendResult{}, callError(CodeBusy, "the session is running a turn")
	}
	conn, agentSession := s.conn, s.state.AgentSessionID
	if s.idle != nil {
		s.idle.Stop()
	}
	s.norm.startTurn(turnID, surface.TimelineItem{Text: params.Text, MessageID: params.MessageID})
	s.state.Status, s.state.TurnID, s.state.LastActive = StatusBusy, turnID, time.Now().UTC()
	s.saveLocked()
	s.mu.Unlock()
	pending, err := conn.Send("session/prompt", acp.PromptParams{SessionID: agentSession, Prompt: []acp.ContentBlock{{Type: "text", Text: params.Text}}})
	if err != nil {
		s.mu.Lock()
		s.norm.endTurn("", "Agenthail could not send the prompt: "+err.Error())
		s.finishTurnLocked("failed")
		s.mu.Unlock()
		s.changed()
		return SendResult{}, callError(CodeFailed, "send the prompt: %v", err)
	}
	s.changed()
	go s.awaitTurn(turnID, pending)
	return SendResult{TurnID: turnID}, nil
}

func (s *session) awaitTurn(turnID string, pending *acp.Pending) {
	var result acp.PromptResult
	err := pending.Wait(context.Background(), &result)
	s.mu.Lock()
	if s.state.TurnID != turnID {
		s.mu.Unlock()
		return
	}
	s.cancelRequestsLocked()
	if err != nil {
		s.norm.endTurn("", "The turn failed: "+err.Error())
	} else {
		s.norm.endTurn(result.StopReason, "")
	}
	s.finishTurnLocked(result.StopReason)
	s.mu.Unlock()
	s.changed()
}

func (s *session) finishTurnLocked(stop string) {
	s.state.LastTurnID, s.state.LastStop = s.state.TurnID, stop
	s.state.TurnID, s.state.Status, s.state.LastActive = "", StatusIdle, time.Now().UTC()
	s.saveLocked()
	if s.idle != nil {
		s.idle.Stop()
	}
	s.idle = time.AfterFunc(idleStop, func() {
		s.startMu.Lock()
		defer s.startMu.Unlock()
		if s.quiet() {
			s.stopProcess()
		}
	})
}

func (s *session) cancel() error {
	s.mu.Lock()
	conn, agentSession, busy := s.conn, s.state.AgentSessionID, s.state.Status == StatusBusy
	s.cancelRequestsLocked()
	s.mu.Unlock()
	if !busy {
		return nil
	}
	if conn == nil {
		return errNotRunning
	}
	return conn.Notify("session/cancel", acp.SessionIDParams{SessionID: agentSession})
}

func (s *session) respond(requestID, choice string) (RespondResult, error) {
	s.mu.Lock()
	pending := s.pending[requestID]
	if pending == nil {
		s.mu.Unlock()
		return RespondResult{}, callError(CodeStale, "this request was already answered or has ended")
	}
	var answer acp.RequestPermissionResult
	outcome := "denied"
	switch choice {
	case "allow":
		index := slices.IndexFunc(pending.request.Options, func(o acp.PermissionOption) bool { return o.Kind == acp.OptionAllowOnce })
		if index < 0 {
			s.mu.Unlock()
			return RespondResult{}, callError(CodeInvalid, "this request offers no way to allow it once")
		}
		answer, outcome = acp.Selected(pending.request.Options[index].OptionID), "allowed"
	case "deny":
		answer = acp.Cancelled()
		if index := slices.IndexFunc(pending.request.Options, func(o acp.PermissionOption) bool { return o.Kind == acp.OptionRejectOnce }); index >= 0 {
			answer = acp.Selected(pending.request.Options[index].OptionID)
		}
	case "stop":
		answer = acp.Cancelled()
	default:
		s.mu.Unlock()
		return RespondResult{}, callError(CodeInvalid, "answer with allow, deny or stop")
	}
	delete(s.pending, requestID)
	written := make(chan error, 1)
	pending.answer <- permissionAnswer{result: answer, written: written}
	conn := s.conn
	s.mu.Unlock()
	s.changed()
	err := awaitWritten(written, conn)
	if choice == "stop" {
		_ = s.cancel()
	}
	if err != nil {
		return RespondResult{}, callError(CodeFailed, "the answer did not reach %s: %v", s.agent.DisplayName(), err)
	}
	return RespondResult{Outcome: outcome}, nil
}

// awaitWritten has no deadline of its own: a write still pending is not a failure, and the caller's own timeout leaves the outcome unknown.
func awaitWritten(written chan error, conn *acp.Conn) error {
	if conn == nil {
		return errNotRunning
	}
	select {
	case err := <-written:
		return err
	case <-conn.Done():
	}
	select {
	case err := <-written:
		return err
	case <-time.After(stopGrace):
		return errors.New("the agent stopped before the answer was written")
	}
}

func (s *session) quiet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Status != StatusBusy && len(s.pending) == 0
}

func (s *session) close(closeAgent, onlyQuiet bool) bool {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return true
	}
	if onlyQuiet && !s.quiet() {
		return false
	}
	if closeAgent {
		s.mu.Lock()
		conn, agentSession, closable := s.conn, s.state.AgentSessionID, s.caps.Session("close")
		s.mu.Unlock()
		if conn != nil && closable {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = conn.Request(ctx, "session/close", map[string]string{"sessionId": agentSession}, nil)
			cancel()
		}
	}
	s.stopProcess()
	s.mu.Lock()
	s.closed = true
	if s.writer != nil {
		s.writer.close()
	}
	s.mu.Unlock()
	s.lock.release()
	return true
}

func (s *session) closedError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return callError(CodeDraining, "this session moved to the current ACP host; send again")
	}
	return nil
}
