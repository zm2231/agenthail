package surfaces

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// StartSession creates the thread on Codex Desktop when its bridge is
// reachable and on Codex's app-server daemon otherwise.
func (c *Codex) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	if client, desktopErr := c.openDesktop(ctx); desktopErr == nil {
		defer client.Close()
		return c.startSessionOnTransport(ctx, client, options, codexTransportDesktop)
	} else if client, managedErr := c.openManaged(ctx); managedErr == nil {
		defer client.Close()
		return c.startSessionOnTransport(ctx, client, options, codexTransportManaged)
	} else {
		return nil, nil, surface.DeliveryUnavailable(fmt.Errorf("no Codex runtime can create the conversation: %v; %v", desktopErr, managedErr))
	}
}

// PrepareManagedTerminalSession creates the provider-owned thread that the
// interactive terminal will resume. It does not start a model turn.
func (c *Codex) PrepareManagedTerminalSession(ctx context.Context, cwd, model string) (*surface.Session, error) {
	if !c.managed {
		return nil, errors.New("managed Codex terminal preparation requires the managed runtime")
	}
	client, err := c.openManaged(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return prepareManagedTerminalSession(ctx, client, cwd, model)
}

func prepareManagedTerminalSession(ctx context.Context, client codexClient, cwd, model string) (*surface.Session, error) {
	params := map[string]any{"threadSource": "agenthail-terminal", "serviceName": "agenthail"}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if model != "" {
		params["model"] = model
	}
	response, err := client.Request(ctx, "thread/start", params, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/start managed terminal: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID := str(thread, "id")
	if threadID == "" {
		return nil, errors.New("thread/start managed terminal returned no thread id")
	}
	session := codexSession(thread)
	session.ID = threadID
	session.Surface = surface.KindCodex
	setCodexTransport(&session, codexTransportManaged)
	session.Source = "agenthail"
	if session.Cwd == "" {
		session.Cwd = str(result, "cwd")
	}
	if session.Cwd == "" {
		session.Cwd = cwd
	}
	return &session, nil
}

func (c *Codex) startSessionOnTransport(ctx context.Context, client codexClient, options surface.SessionStartOptions, transport string) (*surface.Session, *surface.SendResult, error) {
	if err := options.TurnOptions.Validate(surface.KindCodex); err != nil {
		return nil, nil, err
	}
	if options.Worktree != "" || options.Agent != "" || options.PermissionMode != "" || options.Name != "" || options.ConfigRoot != "" {
		return nil, nil, fmt.Errorf("name, worktree, agent, permission-mode and config root creation options require Claude")
	}
	message := strings.TrimSpace(options.Message)
	if message == "" {
		return nil, nil, fmt.Errorf("message is required")
	}
	if err := refuseCodexComposerCommand(options.Message); err != nil {
		return nil, nil, err
	}
	params := map[string]any{"threadSource": "agenthail", "serviceName": "agenthail"}
	if options.Cwd != "" {
		params["cwd"] = options.Cwd
	}
	if options.Model != "" {
		params["model"] = options.Model
	}
	if options.ApprovalPolicy != "" {
		params["approvalPolicy"] = options.ApprovalPolicy
	}
	response, err := client.Request(ctx, "thread/start", params, 10*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("thread/start: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID := str(thread, "id")
	if threadID == "" {
		return nil, nil, fmt.Errorf("thread/start returned no thread id")
	}
	session := &surface.Session{
		ID:         threadID,
		Surface:    surface.KindCodex,
		Name:       surface.DeriveName(str(thread, "name"), message, 80),
		Cwd:        str(result, "cwd"),
		Status:     surface.StatusBusy,
		HasLocal:   true,
		Source:     "agenthail",
		LastActive: time.Now(),
	}
	setCodexTransport(session, transport)
	if session.Cwd == "" {
		session.Cwd = options.Cwd
	}
	turnParams := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": message}},
	}
	if err := applyCodexTurnOptions(ctx, client, session, options.Model, options.TurnOptions, turnParams); err != nil {
		return session, nil, err
	}
	turnResponse, err := client.Request(ctx, "turn/start", turnParams, 10*time.Second)
	if err != nil {
		return session, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/start: %w", err))
	}
	turnID := threadID
	if turnResult, _ := turnResponse["result"].(map[string]any); turnResult != nil {
		if turn, _ := turnResult["turn"].(map[string]any); str(turn, "id") != "" {
			turnID = str(turn, "id")
		} else if value := str(turnResult, "turnId"); value != "" {
			turnID = value
		}
	}
	return session, &surface.SendResult{UUID: turnID, Accepted: true}, nil
}
