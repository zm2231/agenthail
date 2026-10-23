package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type PendingLaunch struct {
	ID         int64
	Launcher   string
	Agent      surface.SurfaceKind
	Cwd, Name  string
	Alias      AliasReservation
	Location   surface.Location
	LaunchedAt time.Time
	ExpiresAt  time.Time
}

// RecordPendingLaunch extends the name's reservation to the launch's expiry.
func (r *Registry) RecordPendingLaunch(launcher string, agent surface.SurfaceKind, cwd, name string, alias AliasReservation, location surface.Location, launchedAt, expires time.Time) (int64, error) {
	b, err := json.Marshal(location)
	if err != nil {
		return 0, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO launcher_pending(launcher,agent,cwd,name,alias,token,expires_at_ms,location,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, launcher, string(agent), cwd, name, alias.Name, alias.Token, expires.UnixMilli(), b, launchedAt.UTC().Format(time.DateTime))
	if err != nil {
		return 0, err
	}
	if alias.Name != "" {
		if _, err := tx.Exec(`UPDATE alias_reservations SET expires_at_ms=? WHERE name=? AND token=?`, expires.UnixMilli(), alias.Name, alias.Token); err != nil {
			return 0, err
		}
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (r *Registry) PendingLaunches() ([]PendingLaunch, error) {
	rows, err := r.read.Query(`SELECT id,launcher,agent,cwd,name,alias,token,expires_at_ms,location,created_at FROM launcher_pending ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PendingLaunch{}
	for rows.Next() {
		var item PendingLaunch
		var launcher, agent, launched string
		var expires int64
		var location []byte
		if err := rows.Scan(&item.ID, &launcher, &agent, &item.Cwd, &item.Name, &item.Alias.Name, &item.Alias.Token, &expires, &location, &launched); err != nil {
			return nil, err
		}
		item.Launcher = launcher
		item.Agent = surface.SurfaceKind(agent)
		item.ExpiresAt = time.UnixMilli(expires)
		item.Alias.Until = item.ExpiresAt
		item.LaunchedAt, _ = time.ParseInLocation(time.DateTime, launched, time.UTC)
		if err := json.Unmarshal(location, &item.Location); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Registry) DropPendingLaunch(item PendingLaunch) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM launcher_pending WHERE id=?`, item.ID); err != nil {
		return err
	}
	if err := releaseAliasReservationTx(tx, item.Alias); err != nil {
		return err
	}
	return tx.Commit()
}

// RegisterSessionAndDeletePending registers the session without a name when
// another writer took the launch's name after its reservation lapsed.
func (r *Registry) RegisterSessionAndDeletePending(s surface.Session, item PendingLaunch) (named bool, err error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM launcher_pending WHERE id=?`, item.ID)
	if err != nil {
		return false, err
	}
	if deleted, err := result.RowsAffected(); err != nil {
		return false, err
	} else if deleted != 1 {
		return false, fmt.Errorf("pending launch %d was not found", item.ID)
	}
	if err := registerSessionTx(tx, s); err != nil {
		return false, err
	}
	named = item.Alias.Name != ""
	if named {
		if err := bindReservedAliasTx(tx, item.Alias, s.ID, r.now()); err != nil {
			if !AliasRefused(err) {
				return false, err
			}
			named = false
		}
	}
	return named, tx.Commit()
}

// AliasRefused reports whether err refuses a name rather than failing to store it.
func AliasRefused(err error) bool {
	var taken AliasTakenError
	var coordinator CoordinatorAliasError
	var reserved AliasReservedError
	return errors.As(err, &taken) || errors.As(err, &coordinator) || errors.As(err, &reserved) || errors.Is(err, ErrReservedAlias)
}
