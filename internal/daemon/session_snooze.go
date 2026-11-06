package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) doSessionSnooze(session *surface.Session, until time.Time, wake string) (actionResult, error) {
	snooze, err := requestedSnooze(session, until, wake, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return actionResult{}, err
	}
	previous, err := d.Registry.SnoozeSession(session.ID, snooze)
	if errors.Is(err, registry.ErrSnoozeArchived) {
		return actionResult{}, conflict(err.Error())
	}
	return d.snoozeResult(session.ID, snoozeRow(snooze), previous, err)
}

func (d *Daemon) doSessionWake(sessionID string) (actionResult, error) {
	previous, err := d.Registry.WakeSession(sessionID)
	return d.snoozeResult(sessionID, nil, previous, err)
}

func requestedSnooze(session *surface.Session, until time.Time, wake string, now time.Time) (registry.Snooze, error) {
	switch {
	case wake == registry.SnoozeWakeFinished && until.IsZero():
		if session.Status != surface.StatusBusy {
			return registry.Snooze{}, conflict("the session is not working, so there is no turn to wait for")
		}
		return registry.Snooze{At: now, Wake: wake}, nil
	case (wake == "" || wake == registry.SnoozeWakeTime) && !until.IsZero():
		until = until.UTC().Truncate(time.Millisecond)
		if !until.After(now) {
			return registry.Snooze{}, actionFailed(http.StatusBadRequest, "bad_request", "until must be in the future")
		}
		return registry.Snooze{At: now, Until: until, Wake: registry.SnoozeWakeTime}, nil
	}
	return registry.Snooze{}, actionFailed(http.StatusBadRequest, "bad_request", `give either until or wake "finished"`)
}

func (d *Daemon) snoozeResult(sessionID string, snooze *dashboardSnooze, previous registry.Snooze, err error) (actionResult, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "session not found")
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	d.requestCatalogDiscovery()
	return actionOK(map[string]any{"ok": true, "sessionId": sessionID, "snooze": snooze, "previous": snoozeRow(previous)}), nil
}
