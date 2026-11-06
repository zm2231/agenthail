package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"time"
)

func (d *Daemon) doSessionPin(pin bool, sessionID string) (actionResult, error) {
	var pinnedAt time.Time
	var err error
	if pin {
		pinnedAt, err = d.Registry.PinSession(sessionID, time.Now().UTC())
	} else {
		err = d.Registry.UnpinSession(sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "session not found")
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	d.requestCatalogDiscovery()
	body := map[string]any{"ok": true, "sessionId": sessionID, "pinned": pin}
	if pin {
		body["pinnedAt"] = pinnedAt
	}
	return actionOK(body), nil
}

// recordLifecycleIntent remembers a stop so the keeper leaves the session
// down, and a resume so it starts over.
func (d *Daemon) recordLifecycleIntent(sessionID, action string) error {
	switch action {
	case "session-lifecycle-stop":
		return d.Registry.RecordSessionStop(sessionID, time.Now().UTC())
	case "session-lifecycle-resume":
		return d.Registry.ClearSessionStop(sessionID)
	}
	return nil
}
