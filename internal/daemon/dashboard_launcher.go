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

func (d *Daemon) dashboardSessionCreate(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	approval := strings.TrimSpace(request.Approval)
	if approval != "" && approval != "untrusted" && approval != "on-request" && approval != "never" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "approval policy is invalid")
		return
	}
	creator := strings.TrimSpace(request.SourceSessionID)
	if creator != "" {
		if _, err := d.Registry.Session(creator); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("sourceSessionId %s is not a session here", creator))
			return
		}
	}
	startOptions := func(cwd string) surface.SessionStartOptions {
		return surface.SessionStartOptions{Message: request.Message, Cwd: cwd, Model: strings.TrimSpace(request.Model), ApprovalPolicy: approval, TurnOptions: request.TurnOptions, Name: request.Name, Worktree: request.Worktree, Agent: request.Agent, PermissionMode: request.PermissionMode, ConfigRoot: strings.TrimSpace(request.ConfigRoot)}
	}
	if request.Launcher != "" {
		if strings.TrimSpace(request.Message) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
			return
		}
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		if alias != "" && (strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must be 1 to 80 characters without spaces, /, or #")
			return
		}
		cwd, cwdErr := dashboardStartCwd(request.Cwd)
		if cwdErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", cwdErr.Error())
			return
		}
		adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
		if adapter == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
			return
		}
		reservation, reserved := d.reserveStartName(w, alias)
		if !reserved {
			return
		}
		keepReservation := false
		defer func() {
			if !keepReservation {
				_ = d.Registry.ReleaseAliasReservation(reservation)
			}
		}()
		if d.transportResolver == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "launcher transport is unavailable")
			return
		}
		launcher, launchErr := d.transportResolver.Require(request.Launcher, surface.SurfaceKind(request.Surface))
		if launchErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", launchErr.Error())
			return
		}
		available, detail := launcher.Available(r.Context())
		if !available {
			writeAPIError(w, http.StatusConflict, "conflict", detail)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		messageUUID := uuid.NewString()
		ctx = surface.WithMessageUUID(ctx, messageUUID)
		launchedAt := time.Now()
		pend := func(location *surface.Location, detail string) {
			if location == nil || *location == (surface.Location{}) {
				d.writeAcceptedLaunch(w, request.Launcher, location, detail, untracked(alias))
				return
			}
			if _, err := d.Registry.RecordPendingLaunch(request.Launcher, surface.SurfaceKind(request.Surface), cwd, request.Name, reservation, *location, launchedAt, launchedAt.Add(pendingLaunchLifetime)); err != nil {
				failure := fmt.Sprintf("persist pending correlation: %s", err)
				if detail != "" {
					failure = detail + "; " + failure
				}
				d.writeAcceptedLaunch(w, request.Launcher, location, failure, untracked(alias))
				return
			}
			keepReservation = true
			if detail != "" {
				d.writeAcceptedLaunch(w, request.Launcher, location, detail, "")
				return
			}
			writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "launcher": request.Launcher, "location": location})
		}
		options := startOptions(cwd)
		pick := d.resolveClaudeStart(ctx, surface.SurfaceKind(request.Surface), &options)
		launchResult, launchErr := launcher.Launch(ctx, surface.LaunchRequest{Surface: surface.SurfaceKind(request.Surface), Options: options})
		startErr := launchErr
		if launchErr != nil && launchResult.Session == nil {
			var acceptedErr surface.LaunchAcceptedError
			switch {
			case errors.Is(launchErr, surface.ErrLaunchOptionUnsupported):
				writeAPIError(w, http.StatusBadRequest, "invalid_request", launchErr.Error())
				return
			case errors.As(launchErr, &acceptedErr):
				pend(launchResult.Location, launchErr.Error())
				return
			default:
				d.writeUnstartedSession(w, request.Message, launchErr)
				return
			}
		}
		session, location, launchErr := locateLaunchedSession(ctx, launcher, adapter, launchResult)
		if launchErr != nil {
			pend(launchResult.Location, fmt.Sprintf("discovery failed: %s", launchErr))
			return
		}
		if session == nil {
			if location == nil {
				d.writeAcceptedLaunch(w, request.Launcher, nil, "location is unresolved", untracked(alias))
				return
			}
			pend(location, "")
			return
		}
		if session.Surface == "" {
			session.Surface = surface.SurfaceKind(request.Surface)
			session.Cwd = cwd
			session.Name = request.Name
		}
		session.Runtime = &surface.Runtime{Launcher: request.Launcher, Location: location, Focusable: location != nil}
		if err := d.Registry.RegisterSession(*session); err != nil {
			d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("register session: %s", err), untracked(alias))
			return
		}
		if err := d.Registry.BindReservedAlias(reservation, session.ID); err != nil {
			d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("persist session name: %s", err), unkeptName(alias))
			return
		}
		if err := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); err != nil {
			d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("record who started the session: %s", err), "Agenthail couldn't record who started it.")
			return
		}
		launchFields := map[string]any{"launcher": request.Launcher, "location": location, "sessionId": session.ID}
		if pick != nil {
			launchFields["account"] = pick
		}
		if launchResult.Session != nil {
			d.writeStartedSessionOutcome(w, session, creator, alias, messageUUID, request.Message, launchResult.Sent, startErr, launchFields)
			return
		}
		launchFields["ok"] = true
		writeDashboardJSON(w, http.StatusCreated, launchFields)
		return
	}
	adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
	starter, ok := adapter.(surface.SessionStarter)
	if adapter == nil || !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "this surface cannot start conversations")
		return
	}
	if strings.TrimSpace(request.Message) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
		return
	}
	cwd, cwdErr := dashboardStartCwd(request.Cwd)
	if cwdErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", cwdErr.Error())
		return
	}
	alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
	reservation, reserved := d.reserveStartName(w, alias)
	if !reserved {
		return
	}
	defer func() { _ = d.Registry.ReleaseAliasReservation(reservation) }()
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	messageUUID := uuid.NewString()
	ctx = surface.WithMessageUUID(ctx, messageUUID)
	options := startOptions(cwd)
	var extra map[string]any
	if pick := d.resolveClaudeStart(ctx, surface.SurfaceKind(request.Surface), &options); pick != nil {
		extra = map[string]any{"account": pick}
	}
	session, sent, startErr := starter.StartSession(ctx, options)
	if session != nil {
		session.Runtime = &surface.Runtime{Launcher: defaultLauncherForSurface(surface.SurfaceKind(request.Surface)), Focusable: false}
		if registerErr := d.Registry.RegisterSession(*session); registerErr != nil {
			writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but local registration failed: %s; do not retry automatically", registerErr), nil)
			return
		}
		if aliasErr := d.Registry.BindReservedAlias(reservation, session.ID); aliasErr != nil {
			writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its name could not be persisted: %s; do not retry automatically", aliasErr), nil)
			return
		}
		if originErr := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); originErr != nil {
			writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but who started it could not be recorded: %s; do not retry automatically", originErr), nil)
			return
		}
		d.writeStartedSessionOutcome(w, session, creator, alias, messageUUID, request.Message, sent, startErr, extra)
		return
	}
	if startErr == nil {
		startErr = errors.New("session starter returned no session")
	}
	d.writeUnstartedSession(w, request.Message, startErr)
	return
}
