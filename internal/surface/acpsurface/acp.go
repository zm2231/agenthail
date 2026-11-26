package acpsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/acp"
	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/surface"
)

type ACP struct {
	surface.MCPAttachment
	agent  acphost.Agent
	client acphost.Client
	store  acphost.Store
}

func New(home string, agent acphost.Agent) *ACP {
	return &ACP{agent: agent, client: acphost.Client{Home: home}, store: acphost.DefaultStore(home)}
}

func Kind(agentID string) surface.RuntimeKind { return surface.RuntimeKind("acp:" + agentID) }

func (a *ACP) Agent() acphost.Agent { return a.agent }

func (a *ACP) Name() surface.SurfaceKind { return surface.SurfaceKind(a.agent.ID) }

func (a *ACP) Label() string { return a.agent.DisplayName() }

func (a *ACP) AgentLabel() string { return a.agent.DisplayName() }

func (a *ACP) LocateProgram() error {
	if a.agent.Installed() {
		return nil
	}
	return fmt.Errorf("no %q command on PATH", a.agent.Command[0])
}

func (a *ACP) RequiredOnAuto() bool { return !a.agent.BuiltIn() }

func (a *ACP) Health(context.Context) error { return a.LocateProgram() }

func (a *ACP) RuntimeKinds() []surface.RuntimeKind { return []surface.RuntimeKind{Kind(a.agent.ID)} }

func (a *ACP) Capabilities() surface.Capabilities {
	return surface.Capabilities{Send: true, Stream: true, Reply: true, Model: true, Interrupt: true, Respond: true, PermissionMode: true}
}

func (a *ACP) session(state acphost.State) surface.Session {
	status := surface.StatusIdle
	if state.Status == acphost.StatusBusy {
		status = surface.StatusBusy
	}
	session := surface.Session{
		ID: state.SessionID, Surface: a.Name(), Name: state.Title, Cwd: state.Cwd, Status: status,
		RuntimeKind: Kind(a.agent.ID), Transport: "acp", Source: "agenthail",
		Transcript: a.store.TranscriptPath(a.agent.ID, state.SessionID), HasLocal: true,
		LastActive: state.LastActive, StartedAt: state.CreatedAt, ConfiguredModel: state.Model,
	}
	if state.Mode != "" || state.Model != "" {
		session.Settings = &surface.ObservedSettings{Model: state.Model, Permission: state.Mode}
	}
	if status == surface.StatusBusy {
		session.WaitingOn = surface.OpenRequestsWaiting(a.requests(state))
	}
	return session
}

func (a *ACP) states(ctx context.Context) ([]acphost.State, error) {
	records, err := a.store.Records(a.agent.ID)
	if err != nil {
		return nil, err
	}
	held, heldErr := a.client.Held(ctx)
	live := map[string]acphost.State{}
	for _, state := range held {
		if state.Agent == a.agent.ID {
			live[state.SessionID] = state
		}
	}
	for i, record := range records {
		if state, ok := live[record.SessionID]; ok {
			records[i] = state
		} else {
			records[i].Status = acphost.StatusIdle
			records[i].TurnID = ""
		}
	}
	if heldErr != nil && !errors.Is(heldErr, acphost.ErrNotRunning) {
		return records, heldErr
	}
	return records, nil
}

func (a *ACP) state(ctx context.Context, id string) (acphost.State, error) {
	if state, found, err := a.client.State(ctx, id); err == nil && found {
		return state, nil
	}
	record, err := a.store.ReadRecord(a.agent.ID, id)
	if err != nil {
		return acphost.State{}, fmt.Errorf("%s session %s: %w", a.agent.DisplayName(), id, surface.ErrSessionNotFound)
	}
	record.Status, record.TurnID = acphost.StatusIdle, ""
	return record, nil
}

func (a *ACP) List(ctx context.Context) ([]surface.Session, error) {
	states, err := a.states(ctx)
	sessions := make([]surface.Session, 0, len(states))
	for _, state := range states {
		sessions = append(sessions, a.session(state))
	}
	return sessions, err
}

func (a *ACP) CatalogList(ctx context.Context) ([]surface.Session, bool, error) {
	sessions, err := a.List(ctx)
	return sessions, true, err
}

func (a *ACP) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	states, err := a.states(ctx)
	if err != nil {
		return nil, err
	}
	var matches []acphost.State
	for _, state := range states {
		if state.SessionID == target {
			session := a.session(state)
			return &session, nil
		}
		if strings.HasPrefix(state.SessionID, target) || (state.Title != "" && strings.EqualFold(state.Title, target)) {
			matches = append(matches, state)
		}
	}
	if len(matches) == 1 {
		session := a.session(matches[0])
		return &session, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%q matches %d %s sessions", target, len(matches), a.agent.DisplayName())
	}
	return nil, fmt.Errorf("no %s session matched %q: %w", a.agent.DisplayName(), target, surface.ErrSessionNotFound)
}

func (a *ACP) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	state, err := a.state(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	observation := &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: state.LastTurnID}
	if state.Status == acphost.StatusBusy {
		observation.Status = surface.StatusBusy
		observation.ActiveTurnID = state.TurnID
		observation.WaitingOn = surface.OpenRequestsWaiting(a.requests(state))
	} else if !state.LastActive.IsZero() && state.LastTurnID != "" {
		observation.CompletedAt = state.LastActive
		if state.LastStop == acp.StopEndTurn {
			observation.SettledAt = state.LastActive
		}
	}
	return observation, nil
}

func (a *ACP) mcpServers(ctx context.Context, sessionID string, fresh bool) ([]acp.MCPServer, func(), error) {
	credentials, err := a.Opening()
	if err != nil {
		return nil, nil, err
	}
	if credentials == nil {
		a.OpenedWithoutMCP(sessionID, surface.ErrMCPServerNotRunning)
		return nil, func() {}, nil
	}
	var token string
	claim := func() {}
	if fresh {
		pending, ref, err := credentials.Pending(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("issue the new session's credential: %w", err)
		}
		token = pending
		claim = func() {
			if err := credentials.Claim(ref, sessionID); err != nil {
				credentials.Unattached(sessionID, fmt.Errorf("claim the new session's credential: %w", err))
			}
		}
	} else if live, ok := credentials.Live(sessionID); ok {
		token = live
	} else if token, err = credentials.Rotate(sessionID); err != nil {
		return nil, nil, fmt.Errorf("renew the session's credential: %w", err)
	}
	server := acp.MCPServer{Type: "http", Name: surface.MCPServerName, URL: credentials.URL(), Headers: []acp.NameValue{{Name: "Authorization", Value: "Bearer " + token}}}
	return []acp.MCPServer{server}, claim, nil
}

func (a *ACP) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return a.SendWithOptions(ctx, sess, message, surface.SendOptions{})
}

func (a *ACP) SendWithOptions(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if !options.TurnOptions.Empty() {
		return nil, surface.DeliveryTerminal(fmt.Errorf("%s takes no turn options", a.agent.DisplayName()), surface.DeliveryInvalidRequest)
	}
	servers, _, err := a.mcpServers(ctx, sess.ID, false)
	if err != nil {
		return nil, err
	}
	if options.Model != "" {
		if _, err := a.client.SetOption(ctx, acphost.OptionParams{SessionID: sess.ID, Category: "model", Value: options.Model}); err != nil {
			return nil, deliveryError(err)
		}
	}
	result, err := a.client.Send(ctx, acphost.SendParams{SessionID: sess.ID, Text: message, MessageID: surface.MessageUUID(ctx), MCPServers: servers})
	if err != nil {
		return nil, deliveryError(err)
	}
	return &surface.SendResult{UUID: result.TurnID, Accepted: true}, nil
}

func deliveryError(err error) error {
	var callErr *acphost.CallError
	switch {
	case errors.Is(err, acphost.ErrNotRunning):
		return surface.DeliveryUnavailable(err)
	case errors.As(err, &callErr) && callErr.Code == acphost.CodeBusy:
		return surface.DeliveryUnavailable(fmt.Errorf("%w: %s", surface.ErrDeliveryHeld, callErr.Message))
	case errors.As(err, &callErr) && (callErr.Code == acphost.CodeInvalid || callErr.Code == acphost.CodeNotFound):
		return surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	case errors.As(err, &callErr) && callErr.Code == acphost.CodeInterrupted:
		return surface.DeliveryUnavailable(fmt.Errorf("%w: %s", surface.ErrInterruptedBeforeSend, callErr.Message))
	case errors.As(err, &callErr) && (callErr.Code == acphost.CodeDraining || callErr.Code == acphost.CodeHeld):
		return surface.DeliveryUnavailable(err)
	}
	return err
}

func (a *ACP) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	if strings.TrimSpace(options.Cwd) == "" {
		return nil, nil, fmt.Errorf("a %s session needs a folder", a.agent.DisplayName())
	}
	if !options.TurnOptions.Empty() || !options.LaunchSettings.Empty() || options.Agent != "" || options.ConfigRoot != "" || options.ApprovalPolicy != "" {
		return nil, nil, fmt.Errorf("%s sessions take a folder, a message, a model and a mode", a.agent.DisplayName())
	}
	id := uuid.NewString()
	servers, claim, err := a.mcpServers(ctx, id, true)
	if err != nil {
		return nil, nil, err
	}
	ceiling := startCeiling(ctx)
	state, err := a.client.Create(ctx, acphost.CreateParams{SessionID: id, Agent: a.agent.ID, Cwd: options.Cwd, Mode: options.PermissionMode, Model: options.Model, Ceiling: ceiling, MCPServers: servers})
	if err != nil {
		return nil, nil, err
	}
	claim()
	session := a.session(state)
	if options.Name != "" {
		session.Name = options.Name
	}
	if err := surface.SessionReady(ctx, session); err != nil {
		_ = a.client.Close(context.Background(), id)
		return nil, nil, err
	}
	result, err := a.client.Send(ctx, acphost.SendParams{SessionID: id, Text: options.Message, MessageID: surface.MessageUUID(ctx), MCPServers: servers})
	if err != nil {
		return &session, nil, deliveryError(err)
	}
	return &session, &surface.SendResult{UUID: result.TurnID, Accepted: true}, nil
}

func (a *ACP) Interrupt(ctx context.Context, sess *surface.Session) error {
	return a.client.Cancel(ctx, sess.ID)
}

func (a *ACP) InterruptTurn(ctx context.Context, sess *surface.Session, turnID string) error {
	state, err := a.state(ctx, sess.ID)
	if err != nil {
		return err
	}
	if state.TurnID != turnID {
		return fmt.Errorf("turn %s is not running", turnID)
	}
	return a.client.Cancel(ctx, sess.ID)
}

func (a *ACP) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if name == "" {
		state, err := a.state(ctx, sess.ID)
		return state.Model, err
	}
	state, err := a.client.SetOption(ctx, acphost.OptionParams{SessionID: sess.ID, Category: "model", Value: name})
	return state.Model, err
}

func (a *ACP) Control(ctx context.Context, sess *surface.Session, control surface.ControlRequest) (*surface.ControlResult, error) {
	result := &surface.ControlResult{SessionID: sess.ID, Action: control.Action, Evidence: surface.EvidenceDelivered, Source: "acp"}
	var err error
	switch control.Action {
	case "interrupt":
		err = a.Interrupt(ctx, sess)
	case "model":
		result.Output, err = a.Model(ctx, sess, strings.TrimSpace(control.Value))
	case "permission-mode":
		_, err = a.client.SetOption(ctx, acphost.OptionParams{SessionID: sess.ID, Category: "mode", Value: strings.TrimSpace(control.Value)})
	default:
		return nil, surface.ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *ACP) Close(ctx context.Context, sess *surface.Session) error {
	return a.client.Close(ctx, sess.ID)
}

func (a *ACP) Steer(context.Context, *surface.Session, string) error { return surface.ErrUnsupported }
func (a *ACP) GoalSet(context.Context, *surface.Session, string) error {
	return surface.ErrUnsupported
}
func (a *ACP) GoalClear(context.Context, *surface.Session) error { return surface.ErrUnsupported }
func (a *ACP) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}
func (a *ACP) Compact(context.Context, *surface.Session) error { return surface.ErrUnsupported }

func (a *ACP) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	return surface.Tail(ctx, a, sess, n)
}

func (a *ACP) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	read, err := a.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: max(limit, 1)})
	if err != nil {
		return nil, err
	}
	if read.Reply == nil {
		return &surface.ReplyResult{}, nil
	}
	return read.Reply, nil
}

func (a *ACP) Requests(ctx context.Context, sess *surface.Session) (surface.RequestState, error) {
	state, err := a.state(ctx, sess.ID)
	if err != nil {
		return surface.RequestState{}, err
	}
	return surface.RequestState{PermissionMode: state.Mode, Requests: a.requests(state)}, nil
}

func (a *ACP) Respond(ctx context.Context, sess *surface.Session, requestID string, decision surface.Decision) (string, error) {
	choice := "deny"
	switch {
	case decision.Always:
		return "", errors.New("this agent's requests are allowed or denied one call at a time")
	case decision.Behavior == "allow":
		choice = "allow"
	case decision.Stop:
		choice = "stop"
	}
	result, err := a.client.Respond(ctx, acphost.RespondParams{SessionID: sess.ID, RequestID: requestID, Choice: choice})
	var callErr *acphost.CallError
	if errors.As(err, &callErr) && callErr.Code == acphost.CodeStale {
		return "", surface.ErrRequestStale
	}
	return result.Outcome, err
}

func (a *ACP) NotifyCatalog(ctx context.Context, changed func()) {
	for ctx.Err() == nil {
		_ = a.client.Subscribe(ctx, func(acphost.Event) { changed() })
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func startCeiling(ctx context.Context) string {
	var ceiling struct {
		Permission string `json:"permission"`
	}
	if raw := surface.StartCeiling(ctx); len(raw) > 0 {
		_ = json.Unmarshal(raw, &ceiling)
	}
	return ceiling.Permission
}

func (a *ACP) newestOptions() acphost.State {
	records, _ := a.store.Records(a.agent.ID)
	var newest acphost.State
	for _, record := range records {
		if record.LastActive.After(newest.LastActive) && len(record.ConfigOptions)+len(record.Modes) > 0 {
			newest = record
		}
	}
	return newest
}

func (a *ACP) Choices() (modes, models []string) {
	newest := a.newestOptions()
	for _, option := range newest.ConfigOptions {
		for _, value := range option.Options {
			switch option.Category {
			case "mode":
				modes = append(modes, value.Value)
			case "model":
				models = append(models, value.Value)
			}
		}
	}
	if len(modes) == 0 {
		for _, mode := range newest.Modes {
			modes = append(modes, mode.ID)
		}
	}
	return modes, models
}

// Models lists the models the agent offered its most recent session; an agent reports them only once a session is open.
func (a *ACP) Models(context.Context) ([]surface.ModelOption, error) {
	models := []surface.ModelOption{}
	for _, option := range a.newestOptions().ConfigOptions {
		if option.Category != "model" {
			continue
		}
		for _, value := range option.Options {
			name := value.Name
			if name == "" {
				name = value.Value
			}
			models = append(models, surface.ModelOption{ID: value.Value, DisplayName: name, Description: value.Description})
		}
	}
	return models, nil
}
