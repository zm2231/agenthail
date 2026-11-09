package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/zm2231/agenthail/internal/surface"
)

// MovedRelay names its target's host from the source host, which names
// itself for its own sessions.
type MovedRelay struct {
	ID          int64  `json:"id"`
	ToSession   string `json:"toSession"`
	ToHost      string `json:"toHost"`
	Pattern     string `json:"pattern"`
	Once        bool   `json:"once,omitempty"`
	Handoff     string `json:"handoff"`
	LastGoalKey string `json:"lastGoalKey,omitempty"`
}

func movedRelays(q rowQuerier, conversation, self string) ([]MovedRelay, error) {
	return queryRows(q, []MovedRelay{}, func(row rowScanner) (MovedRelay, error) {
		var relay MovedRelay
		var once int
		err := row.Scan(&relay.ID, &relay.ToSession, &relay.ToHost, &relay.Pattern, &once, &relay.Handoff, &relay.LastGoalKey)
		relay.Once = once != 0
		if relay.ToHost == "" {
			relay.ToHost = self
		}
		return relay, err
	}, `SELECT r.id,r.to_session,s.host_id,r.pattern,r.once_only,r.handoff,r.last_goal_key FROM routes r JOIN sessions s ON s.id=r.to_session WHERE r.from_session=? AND r.active=1 ORDER BY r.id`, conversation)
}

// relayMoveRefusal refuses a relay with a completion it has not forwarded
// yet: the target starts reading after it and would never forward it.
func relayMoveRefusal(q catalogQuerier, conversation string) (string, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM routes WHERE from_session=? AND active=1 AND channel_id IS NOT NULL ORDER BY id LIMIT 1`, conversation).Scan(&id)
	if err == nil {
		return fmt.Sprintf("relay #%d is tied to a channel; remove it with 'agenthail relay rm %d' first", id, id), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = q.QueryRow(`SELECT r.id FROM routes r JOIN sessions s ON s.id=r.from_session WHERE r.from_session=? AND r.active=1 AND EXISTS(SELECT 1 FROM relay_events e WHERE e.source_key=`+relaySourceKey+` AND e.id>r.event_cursor) ORDER BY r.id LIMIT 1`, conversation).Scan(&id)
	if err == nil {
		return fmt.Sprintf("relay #%d has a completion it has not forwarded yet; move it once the relay has caught up", id), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return "", nil
}

func relaysMovingTx(tx *sql.Tx, sessions ...string) error {
	for _, session := range sessions {
		if moving, err := movingOutTx(tx, session); err != nil {
			return err
		} else if moving {
			return fmt.Errorf("relay source %s is moving to another host; change its relays once the move has finished", session)
		}
	}
	return nil
}

func dropMovedRelaysTx(tx *sql.Tx, conversation string) error {
	key, err := relaySourceKeyOf(tx, conversation)
	if err != nil && !errors.Is(err, errNoRelayTranscript) {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM routes WHERE from_session=?`, conversation); err != nil {
		return err
	}
	if err == nil {
		return dropOrphanRelaySourceTx(tx, key)
	}
	return nil
}

func dropOrphanRelaySourceTx(tx *sql.Tx, key string) error {
	orphaned := `source_key=? AND NOT EXISTS(SELECT 1 FROM routes JOIN sessions s ON s.id=routes.from_session WHERE ` + relaySourceKey + `=?)`
	for _, table := range []string{"relay_sources", "relay_events"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE `+orphaned, key, key); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) CheckMovedRelays(conversation, self string, relays []MovedRelay) error {
	for _, relay := range relays {
		if err := checkMovedRelay(r.read, conversation, self, relay); err != nil {
			return err
		}
	}
	return nil
}

func checkMovedRelay(q catalogQuerier, conversation, self string, relay MovedRelay) error {
	if relay.ToSession == conversation {
		return fmt.Errorf("relay #%d targets the session it relays from", relay.ID)
	}
	if relay.Handoff != HandoffTurnFinished && relay.Handoff != HandoffGoalCompleted {
		return fmt.Errorf("relay #%d has handoff %q, which this host does not know", relay.ID, relay.Handoff)
	}
	if _, err := regexp.Compile(relay.Pattern); err != nil {
		return fmt.Errorf("relay #%d has an invalid pattern: %w", relay.ID, err)
	}
	want := relay.ToHost
	if want == self {
		want = ""
	}
	var owner string
	err := q.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, relay.ToSession).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != want {
		return fmt.Errorf("relay #%d sends to session %s, which this host does not see on the host that runs it; pair that host here first", relay.ID, relay.ToSession)
	}
	return err
}

func insertMovedRelaysTx(tx *sql.Tx, conversation, self string, relays []MovedRelay) error {
	for _, relay := range relays {
		if err := checkMovedRelay(tx, conversation, self, relay); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO routes(from_session,to_session,pattern,once_only,handoff,last_goal_key,event_cursor) VALUES(?,?,?,?,?,?,COALESCE((SELECT MAX(id) FROM relay_events),0))`, conversation, relay.ToSession, relay.Pattern, b2i(relay.Once), relay.Handoff, relay.LastGoalKey); err != nil {
			return err
		}
	}
	return nil
}

// SeedMovedRelaySource reads the source key from session, not its record,
// which is still the source host's mirror until the move is taken.
func (r *Registry) SeedMovedRelaySource(ctx context.Context, reader surface.CompletionReader, session *surface.Session) error {
	key, err := relaySourceKeyFor(session)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		var cursor string
		err := r.read.QueryRow(`SELECT cursor FROM relay_sources WHERE source_key=?`, key).Scan(&cursor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		batch, err := reader.ReadCompletions(ctx, session, cursor)
		if err != nil {
			return err
		}
		err = r.captureRelayCompletionsByKey(key, cursor, batch)
		if !errors.Is(err, ErrRelayCursorMoved) || attempt == 4 {
			return err
		}
	}
}

func relaySourceKeyFor(session *surface.Session) (string, error) {
	if session.Surface != surface.KindClaude {
		return session.ID, nil
	}
	if session.Transcript == "" {
		return "", errNoRelayTranscript
	}
	return "claude:" + session.Transcript, nil
}
