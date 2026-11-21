package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var errNotThePersonsMark = actionFailed(http.StatusForbidden, "person_only", "only the person marks a conversation read or unread")

var personOnlyActions = map[string]error{dismissDecisionAction: errNotThePersonsDismissal, "session-read": errNotThePersonsMark, "session-unread": errNotThePersonsMark}

func (d *Daemon) doSessionReadState(ctx context.Context, p principal, session *surface.Session, unread bool, revision string) (actionResult, error) {
	if !p.forThePerson() {
		return actionResult{}, errNotThePersonsMark
	}
	if revision == "" {
		return actionResult{}, invalidRequest("revision is required: send the readState.revision the conversation showed")
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return actionResult{}, conflict(err.Error())
	}
	writer, ok := adapter.(surface.ReadStateWriter)
	if !ok {
		return actionResult{}, actionFailed(http.StatusBadRequest, "read_state_unsupported", fmt.Sprintf("%s conversations have no read state", session.Surface))
	}
	state, err := writer.SetReadState(ctx, session, unread, revision)
	if changed := (*surface.ReadStateConflict)(nil); errors.As(err, &changed) {
		d.storeReadState(session.ID, changed.Current)
		return actionResult{}, actionFailed(http.StatusConflict, "read_state_conflict", changed.Error())
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "session_operation_failed", err.Error())
	}
	d.storeReadState(session.ID, state)
	return actionOK(map[string]any{"ok": true, "sessionId": session.ID, "readState": state}), nil
}

func (d *Daemon) storeReadState(sessionID string, state surface.ReadState) {
	if err := d.Registry.RecordCodexReadStates(map[string]surface.ReadState{sessionID: state}, time.Now()); err != nil {
		d.log.Printf("read state %s: %s", sessionID, err)
	}
	d.requestCatalogDiscovery()
}
