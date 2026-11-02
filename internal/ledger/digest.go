package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strconv"
)

const eventTimeLayout = "2006-01-02T15:04:05Z"

type LogDigest struct {
	Through int64  `json:"through"`
	Count   int    `json:"count"`
	SHA256  string `json:"sha256"`
}

func (s *Store) positionColumn(projectKey string) (string, error) {
	remote, err := s.RemoteOwner(projectKey)
	if err != nil {
		return "", err
	}
	if remote != "" {
		return "owner_seq", nil
	}
	return "seq", nil
}

func (s *Store) DigestThrough(projectKey string) (int64, error) {
	remote, cursor, err := s.Owner(projectKey)
	if err != nil {
		return 0, err
	}
	if remote != "" {
		if self, err := hostID(s.read); err != nil || remote != self {
			return cursor, err
		}
	}
	return s.LatestSeq(projectKey)
}

func (s *Store) OwnerLogDigest(projectKey string, through int64) (LogDigest, error) {
	if err := requireOwner(s.read, projectKey); err != nil {
		return LogDigest{}, err
	}
	return s.LogDigest(projectKey, through)
}

func (s *Store) LogDigest(projectKey string, through int64) (LogDigest, error) {
	column, err := s.positionColumn(projectKey)
	if err != nil {
		return LogDigest{}, err
	}
	rows, err := s.read.Query(`SELECT `+column+`, actor_host_id, origin_seq, type, ref, actor_session_id, created_at, payload FROM ledger_events
		WHERE project_key=? AND `+column+`>0 AND `+column+`<=? ORDER BY `+column, projectKey, through)
	if err != nil {
		return LogDigest{}, err
	}
	defer rows.Close()
	sum := sha256.New()
	digest := LogDigest{Through: through}
	for rows.Next() {
		var position, origin int64
		var host, typ, ref, actor, at string
		var payload []byte
		if err := rows.Scan(&position, &host, &origin, &typ, &ref, &actor, &at, &payload); err != nil {
			return LogDigest{}, err
		}
		for _, field := range [][]byte{[]byte(strconv.FormatInt(position, 10)), []byte(host), []byte(strconv.FormatInt(origin, 10)), []byte(typ), []byte(ref), []byte(actor), []byte(at), payload} {
			writeField(sum, field)
		}
		digest.Count++
	}
	if err := rows.Err(); err != nil {
		return LogDigest{}, err
	}
	digest.SHA256 = hex.EncodeToString(sum.Sum(nil))
	return digest, nil
}

func writeField(sum hash.Hash, field []byte) {
	var size [binary.MaxVarintLen64]byte
	sum.Write(size[:binary.PutUvarint(size[:], uint64(len(field)))])
	sum.Write(field)
}
