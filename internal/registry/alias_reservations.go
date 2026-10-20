package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AliasReservation lapses at Until, so a starter that dies frees its name.
type AliasReservation struct {
	Name  string
	Token string
	Until time.Time
}

type AliasReservedError struct {
	Name  string
	Until time.Time
}

func (e AliasReservedError) Error() string {
	return fmt.Sprintf("@%s is the name of a conversation being started; choose another name or try again after %s", e.Name, e.Until.Local().Format(time.TimeOnly))
}

func (r *Registry) ReserveAlias(name string, until time.Time) (AliasReservation, error) {
	if name == "" {
		return AliasReservation{}, nil
	}
	if strings.EqualFold(name, LeadAlias) {
		return AliasReservation{}, ErrReservedAlias
	}
	tx, err := r.db.Begin()
	if err != nil {
		return AliasReservation{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM alias_reservations WHERE expires_at_ms<=?`, time.Now().UnixMilli()); err != nil {
		return AliasReservation{}, err
	}
	if coordinated, err := coordinatorNamedTx(tx, name); err != nil {
		return AliasReservation{}, err
	} else if coordinated {
		return AliasReservation{}, CoordinatorAliasError{Name: name}
	}
	var owner string
	err = tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner)
	if err == nil {
		return AliasReservation{}, AliasTakenError{Name: name, Owner: owner}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AliasReservation{}, err
	}
	if err := aliasReservedTx(tx, name, ""); err != nil {
		return AliasReservation{}, err
	}
	reservation := AliasReservation{Name: name, Token: uuid.NewString(), Until: until}
	if _, err := tx.Exec(`INSERT INTO alias_reservations(name,token,expires_at_ms) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET token=excluded.token,expires_at_ms=excluded.expires_at_ms`, name, reservation.Token, until.UnixMilli()); err != nil {
		return AliasReservation{}, err
	}
	return reservation, tx.Commit()
}

// BindReservedAlias still names the session after its reservation lapsed,
// when no other writer has taken the name since.
func (r *Registry) BindReservedAlias(reservation AliasReservation, sessionID string) error {
	if reservation.Name == "" {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := bindReservedAliasTx(tx, reservation, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func bindReservedAliasTx(tx *sql.Tx, reservation AliasReservation, sessionID string) error {
	if err := releaseAliasReservationTx(tx, reservation); err != nil {
		return err
	}
	return setAliasTx(tx, reservation.Name, sessionID)
}

// ReleaseAliasReservation leaves a reservation another start took over after
// this one lapsed.
func (r *Registry) ReleaseAliasReservation(reservation AliasReservation) error {
	if reservation.Name == "" {
		return nil
	}
	_, err := r.db.Exec(`DELETE FROM alias_reservations WHERE name=? AND token=?`, reservation.Name, reservation.Token)
	return err
}

func releaseAliasReservationTx(tx *sql.Tx, reservation AliasReservation) error {
	_, err := tx.Exec(`DELETE FROM alias_reservations WHERE name=? AND token=?`, reservation.Name, reservation.Token)
	return err
}

func aliasReservedTx(tx *sql.Tx, name, token string) error {
	var holder string
	var until int64
	err := tx.QueryRow(`SELECT token,expires_at_ms FROM alias_reservations WHERE name=? AND expires_at_ms>?`, name, time.Now().UnixMilli()).Scan(&holder, &until)
	if errors.Is(err, sql.ErrNoRows) || err == nil && holder == token {
		return nil
	}
	if err != nil {
		return err
	}
	return AliasReservedError{Name: name, Until: time.UnixMilli(until)}
}
