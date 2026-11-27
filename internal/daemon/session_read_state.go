package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A collapsed open outlives the caller that started it, so it is bounded on its own.
const readOpenTimeout = 30 * time.Second

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
	var state *surface.ReadState
	if unread {
		var marked surface.ReadState
		markCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		marked, err = writer.MarkUnread(markCtx, session, revision)
		cancel()
		if err == nil {
			state = &marked
		}
	} else {
		state, err = d.openRead(ctx, writer, session, revision)
	}
	if changed := (*surface.ReadStateConflict)(nil); errors.As(err, &changed) {
		d.storeReadState(session.ID, changed.Current)
		return actionResult{}, actionFailed(http.StatusConflict, "read_state_conflict", changed.Error())
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "session_operation_failed", err.Error())
	}
	body := map[string]any{"ok": true, "sessionId": session.ID}
	if state != nil {
		d.storeReadState(session.ID, *state)
		body["readState"] = *state
	}
	return actionOK(body), nil
}

// Opens of one conversation share one load, and a conversation already read at the revision shown is loaded but not marked again.
func (d *Daemon) openRead(ctx context.Context, writer surface.ReadStateWriter, session *surface.Session, revision string) (*surface.ReadState, error) {
	stored, known, err := d.Registry.CodexReadState(session.ID)
	if err != nil {
		return nil, err
	}
	mark := !known || stored.Unread() || stored.Revision != revision
	key := fmt.Sprintf("%s\x00%s\x00%t", session.ID, revision, mark)
	value, err, _ := d.readOpens.Do(key, func() (any, error) {
		shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), readOpenTimeout)
		defer cancel()
		return writer.OpenRead(shared, session, revision, mark)
	})
	state, _ := value.(*surface.ReadState)
	return state, err
}

func (d *Daemon) storeReadState(sessionID string, state surface.ReadState) {
	if err := d.Registry.RecordCodexReadStates(map[string]surface.ReadState{sessionID: state}, time.Now()); err != nil {
		d.log.Printf("read state %s: %s", sessionID, err)
	}
	d.requestCatalogDiscovery()
}
