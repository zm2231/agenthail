package registry

import (
	"database/sql"
	"errors"
)

const (
	journalStateEnsureSQL       = `INSERT INTO session_journal_state(session_id,next_seq,retained_bytes) VALUES(?,0,0) ON CONFLICT(session_id) DO NOTHING`
	journalNextSeqSQL           = `SELECT next_seq FROM session_journal_state WHERE session_id=?`
	journalByProviderKeySQL     = `SELECT session_id,seq,position,kind,provider_key,payload,observed_at,bytes,body_ref,payload_codec FROM session_journal WHERE session_id=? AND provider_key=? AND provider_key!=''`
	journalUpdateRowSQL         = `UPDATE session_journal SET payload_codec=0,seq=?,kind=?,payload=?,observed_at=?,bytes=?,body_ref=?,payload_role=? WHERE session_id=? AND seq=?`
	journalInsertRowSQL         = `INSERT INTO session_journal(session_id,seq,position,kind,provider_key,payload,observed_at,bytes,body_ref,payload_role) VALUES(?,?,?,?,?,?,?,?,?,?)`
	journalStateAdvanceSQL      = `UPDATE session_journal_state SET next_seq=?,retained_bytes=retained_bytes+? WHERE session_id=?`
	journalRetainedSQL          = `SELECT (SELECT COUNT(*) FROM session_journal WHERE session_id=?),retained_bytes FROM session_journal_state WHERE session_id=?`
	journalBodySQL              = `SELECT body FROM session_journal_bodies WHERE session_id=? AND ref=?`
	journalBodyTailSQL          = `SELECT chunk FROM session_journal_body_tail WHERE ref=? ORDER BY start`
	journalBodyClearTailSQL     = `DELETE FROM session_journal_body_tail WHERE session_id=? AND ref=?`
	journalBodyStoreSQL         = `INSERT INTO session_journal_bodies(ref,session_id,body,created_at,written_seq) VALUES(?,?,?,?,?) ON CONFLICT(ref) DO UPDATE SET body=excluded.body,session_id=excluded.session_id,created_at=excluded.created_at,written_seq=excluded.written_seq`
	journalBodyStoredLengthSQL  = `SELECT COALESCE((SELECT start+length(chunk) FROM session_journal_body_tail WHERE ref=?1 ORDER BY start DESC LIMIT 1),length(CAST(body AS BLOB))) FROM session_journal_bodies WHERE session_id=?2 AND ref=?1`
	journalBodyAppendTailSQL    = `INSERT INTO session_journal_body_tail(ref,session_id,start,chunk) VALUES(?,?,?,?)`
	journalUpdateAppendedRowSQL = `UPDATE session_journal SET payload_codec=0,seq=?,kind=?,payload=?,observed_at=?,bytes=?,payload_role=? WHERE session_id=? AND seq=?`
)

var journalStatementSQL = []string{
	journalStateEnsureSQL, journalNextSeqSQL, journalByProviderKeySQL, journalUpdateRowSQL, journalInsertRowSQL,
	journalStateAdvanceSQL, journalRetainedSQL, journalBodySQL, journalBodyTailSQL, journalBodyClearTailSQL,
	journalBodyStoreSQL, journalBodyStoredLengthSQL, journalBodyAppendTailSQL, journalUpdateAppendedRowSQL,
}

// Statements are prepared once, after any migration, because the writer pool
// holds a single connection: preparing inside a transaction on r.db would
// wait for the connection that transaction holds.
type journalStatements map[string]*sql.Stmt

func prepareJournalStatements(db *sql.DB) (journalStatements, error) {
	prepared := journalStatements{}
	for _, query := range journalStatementSQL {
		stmt, err := db.Prepare(query)
		if err != nil {
			return nil, errors.Join(err, prepared.close())
		}
		prepared[query] = stmt
	}
	return prepared, nil
}

func (s journalStatements) close() error {
	var errs []error
	for _, stmt := range s {
		errs = append(errs, stmt.Close())
	}
	return errors.Join(errs...)
}

type journalWriter interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type journalTx struct {
	*sql.Tx
	prepared journalStatements
}

func (r *Registry) beginJournal() (journalTx, error) {
	tx, err := r.db.Begin()
	return journalTx{Tx: tx, prepared: r.journal}, err
}

func (t journalTx) Exec(query string, args ...any) (sql.Result, error) {
	if stmt := t.prepared[query]; stmt != nil {
		return t.Tx.Stmt(stmt).Exec(args...)
	}
	return t.Tx.Exec(query, args...)
}

func (t journalTx) Query(query string, args ...any) (*sql.Rows, error) {
	if stmt := t.prepared[query]; stmt != nil {
		return t.Tx.Stmt(stmt).Query(args...)
	}
	return t.Tx.Query(query, args...)
}

func (t journalTx) QueryRow(query string, args ...any) *sql.Row {
	if stmt := t.prepared[query]; stmt != nil {
		return t.Tx.Stmt(stmt).QueryRow(args...)
	}
	return t.Tx.QueryRow(query, args...)
}
