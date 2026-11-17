package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// SessionFields names fields of a session row: one bit per sessions column,
// and one for the session's runtime record.
type SessionFields uint64

// SessionRuntimeField is the session's runtime record: launcher, location
// and whether it can be focused.
const SessionRuntimeField SessionFields = 1 << 63

func sessionColumnFields(names ...string) SessionFields {
	var fields SessionFields
	for _, name := range names {
		index := slices.IndexFunc(sessionWriteColumns, func(column sessionWriteColumn) bool { return column.name == name })
		if index < 0 {
			panic("registry: no sessions column " + name)
		}
		fields |= 1 << index
	}
	return fields
}

var (
	// SessionActivityFields are status, ended and last_active_ms.
	SessionActivityFields = sessionColumnFields("status", "ended", "last_active_ms")
	// SessionProcessFields are pid, its process identity, and bridge.
	SessionProcessFields = sessionColumnFields("pid", "pid_identity", "bridge")
	// SessionTransportField is transport.
	SessionTransportField = sessionColumnFields("transport")
	// An update that guards runtime_kind is refused by its guard when the
	// row is hosted and its read was not; only a write of runtime_kind needs
	// the hosted-runtime check.
	sessionRuntimeKindField = sessionColumnFields("runtime_kind")
	// AllSessionFields is every column and the runtime record.
	AllSessionFields = SessionFields(1)<<len(sessionWriteColumns) - 1 | SessionRuntimeField
)

// SessionUpdate is a write to a session row a writer read earlier as From.
// It writes only the Owns fields, and only while every Guard field still
// holds what From held; otherwise it fails with ErrSessionChanged and the
// writer reads the row again. Registering a whole row is for a session the
// caller creates or has just observed; every other write is a SessionUpdate.
type SessionUpdate struct {
	From  surface.Session
	Owns  SessionFields
	Guard SessionFields
}

// ErrSessionChanged is an update refused because the session's stored row
// changed after the writer read it.
var ErrSessionChanged = errors.New("the session changed after it was read")

// OwnedSessionUpdate writes owns and requires every other field unchanged,
// for a writer that decides its fields from the whole row it read.
func OwnedSessionUpdate(from surface.Session, owns SessionFields) SessionUpdate {
	return SessionUpdate{From: from, Owns: owns, Guard: AllSessionFields &^ owns}
}

// EditedSessionUpdate writes an edit a person or agent made to a session
// read as from, and only while the fields it edits still hold what it read,
// so a different edit made meanwhile is not overwritten.
func EditedSessionUpdate(from, to surface.Session) SessionUpdate {
	changed := changedSessionFields(from, to)
	return SessionUpdate{From: from, Owns: changed, Guard: changed}
}

// LearnedSessionUpdate writes what an action on a session read as from has
// just learned from its process or provider: only the fields that changed,
// whatever else was written meanwhile, since the action's values are the
// newest for those fields.
func LearnedSessionUpdate(from, to surface.Session) SessionUpdate {
	return SessionUpdate{From: from, Owns: changedSessionFields(from, to)}
}

// A session pinned to a holder process is reached through that holder for
// one delivery, and its row keeps its own process, so nothing about the pin
// is written.
func changedSessionFields(from, to surface.Session) SessionFields {
	var changed SessionFields
	if to.HolderPID > 0 {
		return 0
	}
	for index, column := range sessionWriteColumns {
		if column.value(to) != column.value(from) {
			changed |= 1 << index
		}
	}
	if to.Runtime != nil && !reflect.DeepEqual(to.Runtime, from.Runtime) {
		changed |= SessionRuntimeField
	}
	if !slices.EqualFunc(to.Holders, from.Holders, func(a, b surface.Holder) bool { return a.PID == b.PID }) {
		changed |= SessionProcessFields
	}
	return changed
}

func (f SessionFields) columns() []sessionWriteColumn {
	var columns []sessionWriteColumn
	for index, column := range sessionWriteColumns {
		if f&(1<<index) != 0 {
			columns = append(columns, column)
		}
	}
	return columns
}

// UpdateSession writes update's fields of session to its stored row. A
// session a paired host serves is refused with SessionOwnedError, even when
// the update writes nothing.
func (r *Registry) UpdateSession(session surface.Session, update SessionUpdate) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	if err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, session.ID).Scan(&owner); err == sql.ErrNoRows {
		return ErrSessionChanged
	} else if err != nil {
		return err
	}
	if owner != "" {
		if err := releaseUnservedHostRowTx(tx, session.ID, owner); err != nil {
			return err
		}
	}
	if update.Owns == 0 {
		return tx.Commit()
	}
	if err := updateSessionTx(tx, session, update); err != nil {
		return err
	}
	if err := bindClaimedCredentialTx(tx, session.ID, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

func updateSessionTx(tx *sql.Tx, s surface.Session, update SessionUpdate) error {
	if s.ID != update.From.ID {
		return fmt.Errorf("update of session %s was read from session %s", s.ID, update.From.ID)
	}
	for index, column := range sessionWriteColumns {
		if update.Owns&(1<<index) == 0 && column.value(s) != column.value(update.From) {
			return fmt.Errorf("update of session %s changes %s, which it does not own", s.ID, column.name)
		}
	}
	result, err := tx.Exec(ownedSessionUpdateSQL(update.Owns.columns(), update.Guard.columns()), append(sessionWriteValues(s), sessionWriteValues(update.From)...)...)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return changedSessionColumnsTx(tx, s, update)
	}
	if update.Owns&SessionRuntimeField != 0 && s.Runtime != nil {
		if err := upsertSessionRuntimeTx(tx, s); err != nil {
			return err
		}
	}
	if update.Owns&SessionProcessFields != 0 {
		return releaseClaudePIDsTx(tx, s)
	}
	return nil
}

// changedSessionColumnsTx names the guarded columns that no longer hold what
// the writer read, or the row's absence, in the ErrSessionChanged it returns.
func changedSessionColumnsTx(tx *sql.Tx, s surface.Session, update SessionUpdate) error {
	var moved []string
	for _, column := range update.Guard.columns() {
		var held bool
		err := tx.QueryRow(`SELECT `+ownedSessionGuardSQL(column)+` FROM sessions, `+strings.Replace(sessionExcludedSQL, ") AS excluded", ") AS next", 1)+`, `+sessionExcludedSQL+` WHERE sessions.id=next.id AND sessions.id=excluded.id`, append(sessionWriteValues(s), sessionWriteValues(update.From)...)...).Scan(&held)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: it is no longer recorded", ErrSessionChanged)
		}
		if err != nil {
			return err
		}
		if !held {
			moved = append(moved, column.name)
		}
	}
	return fmt.Errorf("%w: %s changed", ErrSessionChanged, strings.Join(moved, ", "))
}
