package registry

import (
	"fmt"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	journalCodecPlain = 0
	journalCodecZstd  = 1

	// Message and text rows stay plain: the prompt columns and indexes read them as JSON.
	journalCompressedKinds = `('toolResult','toolCall','event')`
	journalReclaimPages    = 2000
	autoVacuumIncremental  = 2
)

var journalDecoder = sync.OnceValue(func() *zstd.Decoder {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
	return decoder
})

var journalEncoder = sync.OnceValue(func() *zstd.Encoder {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		panic(err)
	}
	return encoder
})

func readJournalPayload(codec int, stored []byte) ([]byte, error) {
	switch codec {
	case journalCodecPlain:
		return append([]byte(nil), stored...), nil
	case journalCodecZstd:
		plain, err := journalDecoder().DecodeAll(stored, nil)
		if err != nil {
			return nil, fmt.Errorf("decompress session journal row: %w", err)
		}
		return plain, nil
	}
	return nil, fmt.Errorf("session journal row has unknown codec %d", codec)
}

// JournalCompression is what compressing a session's journal changed.
type JournalCompression struct {
	Rows   int
	Plain  int64
	Packed int64
}

type packedJournalRows struct {
	sessionID  string
	throughSeq int64
	rows       []packedJournalRow
	summary    JournalCompression
}

type packedJournalRow struct {
	seq     int64
	payload []byte
}

// IdleJournalSessions lists sessions whose newest row predates idleSince and that have rows no sweep has looked at.
func (r *Registry) IdleJournalSessions(idleSince time.Time) ([]string, error) {
	rows, err := r.read.Query(`SELECT st.session_id FROM session_journal_state st
		JOIN session_journal j ON j.session_id=st.session_id
			AND j.seq=(SELECT MAX(seq) FROM session_journal WHERE session_id=st.session_id)
		WHERE st.swept_seq<st.next_seq AND j.observed_at<?`, formatTimestamp(idleSince))
	if err != nil {
		return nil, err
	}
	sessions, err := appendRows([]string{}, rows, func(row rowScanner) (string, error) {
		var session string
		err := row.Scan(&session)
		return session, err
	})
	return sessions, err
}

// CompressJournalSession changes no seq, position, observed_at or bytes, so stream cursors, retention and idleness read the same afterwards.
func (r *Registry) CompressJournalSession(sessionID string) (JournalCompression, error) {
	packed, err := r.packJournalRows(sessionID)
	if err != nil {
		return JournalCompression{}, err
	}
	return r.storePackedJournalRows(packed)
}

func (r *Registry) packJournalRows(sessionID string) (packedJournalRows, error) {
	packed := packedJournalRows{sessionID: sessionID}
	if err := r.read.QueryRow(`SELECT next_seq FROM session_journal_state WHERE session_id=?`, sessionID).Scan(&packed.throughSeq); err != nil {
		return packed, err
	}
	rows, err := r.read.Query(`SELECT seq,payload FROM session_journal WHERE session_id=? AND seq<=? AND payload_codec=0 AND kind IN `+journalCompressedKinds, sessionID, packed.throughSeq)
	if err != nil {
		return packed, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var payload []byte
		if err := rows.Scan(&seq, &payload); err != nil {
			return packed, err
		}
		compressed := journalEncoder().EncodeAll(payload, nil)
		if len(compressed) >= len(payload) {
			continue
		}
		packed.rows = append(packed.rows, packedJournalRow{seq: seq, payload: compressed})
		packed.summary.Plain += int64(len(payload))
		packed.summary.Packed += int64(len(compressed))
	}
	return packed, rows.Err()
}

// An upsert gives a row a new seq, so one that moved after packing no longer matches and stays as written.
func (r *Registry) storePackedJournalRows(packed packedJournalRows) (JournalCompression, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return JournalCompression{}, err
	}
	defer tx.Rollback()
	summary := packed.summary
	summary.Rows = 0
	for _, row := range packed.rows {
		result, err := tx.Exec(`UPDATE session_journal SET payload=?,payload_codec=? WHERE session_id=? AND seq=? AND payload_codec=0`, row.payload, journalCodecZstd, packed.sessionID, row.seq)
		if err != nil {
			return JournalCompression{}, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return JournalCompression{}, err
		}
		summary.Rows += int(changed)
	}
	if _, err := tx.Exec(`UPDATE session_journal_state SET swept_seq=MAX(swept_seq,?) WHERE session_id=?`, packed.throughSeq, packed.sessionID); err != nil {
		return JournalCompression{}, err
	}
	return summary, tx.Commit()
}

// ReclaimFreePages returns free pages to the disk a step at a time and reports how many are left. A registry made before incremental vacuum has nothing to do here: its free pages are reused.
func (r *Registry) ReclaimFreePages() (int64, error) {
	var mode int
	if err := r.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return 0, err
	}
	if mode != autoVacuumIncremental {
		return 0, nil
	}
	rows, err := r.db.Query(fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, journalReclaimPages))
	if err != nil {
		return 0, err
	}
	for rows.Next() {
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var left int64
	if err := r.db.QueryRow(`PRAGMA freelist_count`).Scan(&left); err != nil {
		return 0, err
	}
	return left, nil
}
