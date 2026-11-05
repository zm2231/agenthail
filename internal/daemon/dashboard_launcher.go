package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) doSessionCreate(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	approval := strings.TrimSpace(request.Approval)
	if approval != "" && approval != "untrusted" && approval != "on-request" && approval != "never" {
		return actionResult{}, invalidRequest("approval policy is invalid")
	}
	creator := strings.TrimSpace(request.SourceSessionID)
	if creator != "" {
		if _, err := d.Registry.Session(creator); err != nil {
			return actionResult{}, invalidRequest(fmt.Sprintf("sourceSessionId %s is not a session here", creator))
		}
	}
	startOptions := func(cwd string) surface.SessionStartOptions {
		return surface.SessionStartOptions{Message: request.Message, Cwd: cwd, Model: strings.TrimSpace(request.Model), ApprovalPolicy: approval, TurnOptions: request.TurnOptions, Name: request.Name, Worktree: request.Worktree, Agent: request.Agent, PermissionMode: request.PermissionMode, ConfigRoot: strings.TrimSpace(request.ConfigRoot)}
	}
	if request.Task && creator == "" {
		return actionResult{}, invalidRequest("a task needs the session that started it")
	}
	if request.Launcher != "" {
		if request.Task {
			return actionResult{}, invalidRequest("a session started in a terminal cannot be a task")
		}
		return d.launchSession(ctx, request, creator, startOptions)
	}
	adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
	starter, ok := adapter.(surface.SessionStarter)
	if adapter == nil || !ok {
		return actionResult{}, invalidRequest("this surface cannot start conversations")
	}
	if strings.TrimSpace(request.Message) == "" {
		return actionResult{}, invalidRequest("message is required")
	}
	cwd, cwdErr := dashboardStartCwd(request.Cwd)
	if cwdErr != nil {
		return actionResult{}, invalidRequest(cwdErr.Error())
	}
	alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
	reservation, err := d.reserveName(alias)
	if err != nil {
		return actionResult{}, err
	}
	defer func() { _ = d.Registry.ReleaseAliasReservation(reservation) }()
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	messageUUID := uuid.NewString()
	ctx = surface.WithMessageUUID(ctx, messageUUID)
	ctx = surface.WithSourceSessionID(ctx, surface.SendOptions{SourceSessionID: creator, Author: surface.AuthorPerson}.Sender())
	if len(request.Ceiling) > 0 {
		ctx = surface.WithStartCeiling(ctx, request.Ceiling)
	}
	options := startOptions(cwd)
	var extra map[string]any
	pick, placeErr := d.resolveClaudeStart(surface.SurfaceKind(request.Surface), creator, &options)
	if placeErr != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", placeErr.Error())
	}
	if pick != nil {
		extra = map[string]any{"account": pick}
	}
	session, sent, startErr := starter.StartSession(ctx, options)
	if session == nil {
		if startErr == nil {
			startErr = errors.New("session starter returned no session")
		}
		return actionResult{}, d.unstartedSession(request.Message, startErr)
	}
	session.Runtime = &surface.Runtime{Launcher: defaultLauncherForSurface(surface.SurfaceKind(request.Surface)), Focusable: false}
	if registerErr := d.Registry.RegisterSession(*session); registerErr != nil {
		return actionResult{}, createStorageFailure(session, fmt.Sprintf("session was created, but local registration failed: %s; do not retry automatically", registerErr), nil)
	}
	if aliasErr := d.Registry.BindReservedAlias(reservation, session.ID); aliasErr != nil {
		return actionResult{}, createStorageFailure(session, fmt.Sprintf("session was created, but its name could not be persisted: %s; do not retry automatically", aliasErr), nil)
	}
	if originErr := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); originErr != nil {
		return actionResult{}, createStorageFailure(session, fmt.Sprintf("session was created, but who started it could not be recorded: %s; do not retry automatically", originErr), nil)
	}
	if request.Task {
		task, taskErr := d.Registry.CreateTask(creator, session.ID, request.Message, time.Now())
		if taskErr != nil {
			return actionResult{}, createStorageFailure(session, fmt.Sprintf("session was created, but its task could not be recorded: %s; do not retry automatically", taskErr), nil)
		}
		if extra == nil {
			extra = map[string]any{}
		}
		extra["taskId"] = task.ID
	}
	result, err := d.startedSessionOutcome(session, creator, alias, messageUUID, request.Message, sent, startErr, extra)
	if request.Task && err != nil {
		if _, settleErr := d.settleTask(session.ID, registry.TaskSettlement{State: registry.TaskFailed, Kind: registry.TaskKindTurn, Result: startErr.Error()}); settleErr != nil {
			d.log.Printf("settle task %s: %s", d.resolveDisplay(session.ID), settleErr)
		}
	}
	return result, err
}

func (d *Daemon) launchSession(ctx context.Context, request dashboardActionRequest, creator string, startOptions func(string) surface.SessionStartOptions) (actionResult, error) {
	if strings.TrimSpace(request.Message) == "" {
		return actionResult{}, invalidRequest("message is required")
	}
	alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
	if alias != "" && (strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80) {
		return actionResult{}, invalidRequest("name must be 1 to 80 characters without spaces, /, or #")
	}
	cwd, cwdErr := dashboardStartCwd(request.Cwd)
	if cwdErr != nil {
		return actionResult{}, invalidRequest(cwdErr.Error())
	}
	adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
	if adapter == nil {
		return actionResult{}, conflict("surface is not configured")
	}
	reservation, err := d.reserveName(alias)
	if err != nil {
		return actionResult{}, err
	}
	keepReservation := false
	defer func() {
		if !keepReservation {
			_ = d.Registry.ReleaseAliasReservation(reservation)
		}
	}()
	if d.transportResolver == nil {
		return actionResult{}, conflict("launcher transport is unavailable")
	}
	launcher, launchErr := d.transportResolver.Require(request.Launcher, surface.SurfaceKind(request.Surface))
	if launchErr != nil {
		return actionResult{}, invalidRequest(launchErr.Error())
	}
	if available, detail := launcher.Available(ctx); !available {
		return actionResult{}, conflict(detail)
	}
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	messageUUID := uuid.NewString()
	ctx = surface.WithMessageUUID(ctx, messageUUID)
	launchedAt := time.Now()
	pend := func(location *surface.Location, detail string) actionResult {
		if location == nil || *location == (surface.Location{}) {
			return d.acceptedLaunch(request.Launcher, location, detail, untracked(alias))
		}
		if _, err := d.Registry.RecordPendingLaunch(request.Launcher, surface.SurfaceKind(request.Surface), cwd, request.Name, reservation, *location, launchedAt, launchedAt.Add(pendingLaunchLifetime)); err != nil {
			failure := fmt.Sprintf("persist pending correlation: %s", err)
			if detail != "" {
				failure = detail + "; " + failure
			}
			return d.acceptedLaunch(request.Launcher, location, failure, untracked(alias))
		}
		keepReservation = true
		if detail != "" {
			return d.acceptedLaunch(request.Launcher, location, detail, "")
		}
		return actionResult{Status: http.StatusCreated, Body: map[string]any{"ok": true, "launcher": request.Launcher, "location": location}}
	}
	options := startOptions(cwd)
	pick, placeErr := d.resolveClaudeStart(surface.SurfaceKind(request.Surface), creator, &options)
	if placeErr != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", placeErr.Error())
	}
	launchRequest := surface.LaunchRequest{Surface: surface.SurfaceKind(request.Surface), Options: options}
	if credentials := d.mcpCredentials.Load(); credentials != nil {
		mcp, err := credentials.terminalLaunch(request.Launcher, launchRequest.Surface)
		if err != nil {
			return actionResult{}, d.unstartedSession(request.Message, fmt.Errorf("issue the session's MCP credential: %w", err))
		}
		launchRequest.MCP = mcp
	}
	launchResult, launchErr := launcher.Launch(ctx, launchRequest)
	startErr := launchErr
	if launchErr != nil && launchResult.Session == nil {
		var acceptedErr surface.LaunchAcceptedError
		switch {
		case errors.Is(launchErr, surface.ErrLaunchOptionUnsupported):
			return actionResult{}, invalidRequest(launchErr.Error())
		case errors.As(launchErr, &acceptedErr):
			return pend(launchResult.Location, launchErr.Error()), nil
		default:
			return actionResult{}, d.unstartedSession(request.Message, launchErr)
		}
	}
	session, location, locateErr := locateLaunchedSession(ctx, launcher, adapter, launchResult)
	if locateErr != nil {
		return pend(launchResult.Location, fmt.Sprintf("discovery failed: %s", locateErr)), nil
	}
	if session == nil {
		if location == nil {
			return d.acceptedLaunch(request.Launcher, nil, "location is unresolved", untracked(alias)), nil
		}
		return pend(location, ""), nil
	}
	if session.Surface == "" {
		session.Surface = surface.SurfaceKind(request.Surface)
		session.Cwd = cwd
		session.Name = request.Name
	}
	session.Runtime = &surface.Runtime{Launcher: request.Launcher, Location: location, Focusable: location != nil}
	if err := d.Registry.RegisterSession(*session); err != nil {
		return d.acceptedLaunch(request.Launcher, location, fmt.Sprintf("register session: %s", err), untracked(alias)), nil
	}
	if err := d.Registry.BindReservedAlias(reservation, session.ID); err != nil {
		return d.acceptedLaunch(request.Launcher, location, fmt.Sprintf("persist session name: %s", err), unkeptName(alias)), nil
	}
	if err := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); err != nil {
		return d.acceptedLaunch(request.Launcher, location, fmt.Sprintf("record who started the session: %s", err), "Agenthail couldn't record who started it."), nil
	}
	launchFields := map[string]any{"launcher": request.Launcher, "location": location, "sessionId": session.ID}
	if pick != nil {
		launchFields["account"] = pick
	}
	if launchResult.Session != nil {
		return d.startedSessionOutcome(session, creator, alias, messageUUID, request.Message, launchResult.Sent, startErr, launchFields)
	}
	launchFields["ok"] = true
	return actionResult{Status: http.StatusCreated, Body: launchFields}, nil
}
