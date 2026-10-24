package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) LookupAlias(name string) (string, error) {
	var sid string
	err := r.read.QueryRow(`SELECT session_id FROM aliases WHERE name = ?`, name).Scan(&sid)
	return sid, err
}

func (r *Registry) SetAlias(name, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setAliasTx(tx, name, sessionID, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

type AliasTakenError struct {
	Name  string
	Owner string
}

func (e AliasTakenError) Error() string {
	return fmt.Sprintf("alias %q is already assigned to session %q", e.Name, e.Owner)
}

func setAliasTx(tx *sql.Tx, name, sessionID string, now time.Time) error {
	if strings.EqualFold(name, LeadAlias) {
		return ErrReservedAlias
	}
	var current string
	err := tx.QueryRow(`SELECT name FROM aliases WHERE session_id=?`, sessionID).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if current == name {
		return nil
	}
	for _, held := range []string{name, current} {
		if held == "" {
			continue
		}
		if coordinated, err := coordinatorNamedTx(tx, held); err != nil {
			return err
		} else if coordinated {
			return CoordinatorAliasError{Name: held}
		}
	}
	if err := aliasReservedTx(tx, name, "", now); err != nil {
		return err
	}
	return bindAliasTx(tx, name, sessionID)
}

func bindAliasTx(tx *sql.Tx, name, sessionID string) error {
	if strings.EqualFold(name, LeadAlias) {
		return ErrReservedAlias
	}
	var owner string
	err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && owner != sessionID {
		return AliasTakenError{Name: name, Owner: owner}
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?)`, name, sessionID)
	return err
}

func (r *Registry) ReserveGeneratedAlias(sessionID, base string) (string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existing string
	if err := tx.QueryRow(`SELECT name FROM aliases WHERE session_id=?`, sessionID).Scan(&existing); err == nil {
		return existing, tx.Commit()
	} else if err != sql.ErrNoRows {
		return "", err
	}
	base = generatedAliasCharacters.ReplaceAllString(strings.ToLower(base), "-")
	base = strings.Trim(base, "- ")
	if base == "" {
		base = "agent"
	}
	for n := 0; n < 1000; n++ {
		candidate := base
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d", base, n+1)
		}
		if candidate == LeadAlias {
			continue
		}
		if coordinated, err := coordinatorNamedTx(tx, candidate); err != nil {
			return "", err
		} else if coordinated {
			continue
		}
		var reserved AliasReservedError
		if err := aliasReservedTx(tx, candidate, "", r.now()); errors.As(err, &reserved) {
			continue
		} else if err != nil {
			return "", err
		}
		if _, err := tx.Exec(`INSERT INTO aliases(name,session_id) VALUES(?,?)`, candidate, sessionID); err == nil {
			return candidate, tx.Commit()
		} else if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", fmt.Errorf("reserve generated alias %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("reserve generated alias: exhausted candidates for %q", base)
}

type AliasRow struct {
	Name      string
	SessionID string
}

func (r *Registry) ListAliases() ([]AliasRow, error) {
	rows, err := r.read.Query(`SELECT name, session_id FROM aliases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AliasRow
	for rows.Next() {
		var a AliasRow
		if err := rows.Scan(&a.Name, &a.SessionID); err == nil {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

func (r *Registry) ReverseAlias(sessionID string) (string, error) {
	var name string
	err := r.read.QueryRow(`SELECT name FROM aliases WHERE session_id = ?`, sessionID).Scan(&name)
	return name, err
}

func (r *Registry) ReplyTarget(sessionID string) (string, error) {
	var alias, hostName, hostAlias string
	err := r.read.QueryRow(`SELECT CASE WHEN s.host_id='' THEN COALESCE((SELECT a.name FROM aliases a WHERE a.session_id=s.id),'') ELSE '' END,
		COALESCE(h.name,''),
		COALESCE((SELECT json_extract(cs.projection_fingerprint,'$.alias') FROM catalog_sessions cs WHERE cs.session_id=s.id AND json_valid(cs.projection_fingerprint)),'')
		FROM sessions s LEFT JOIN hosts h ON h.host_id=s.host_id WHERE s.id=? AND s.runtime_kind<>?`, sessionID, string(surface.RuntimeOperator)).Scan(&alias, &hostName, &hostAlias)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	case alias != "":
		return "@" + alias, nil
	case hostName != "" && hostAlias != "":
		return hostName + "::@" + hostAlias, nil
	}
	return "", nil
}

func (r *Registry) RemoveAlias(name string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if coordinated, err := coordinatorNamedTx(tx, name); err != nil {
		return err
	} else if coordinated {
		return CoordinatorAliasError{Name: name}
	}
	res, err := tx.Exec(`DELETE FROM aliases WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("alias %q does not exist", name)
	}
	return tx.Commit()
}
