package registry

const (
	MoveShownHolding  = "holding"
	MoveShownCopying  = "copying"
	MoveShownSettling = "settling"
)

type MoveShown struct {
	ID       string
	State    string
	PeerHost string
	HostName string
	Root     string
	Detail   string
}

const moveShownQuery = `SELECT m.conversation,m.id,m.state,m.outcome_unknown_at<>'',m.peer_host,COALESCE(h.name,''),
 COALESCE(json_extract(CAST(m.manifest AS TEXT),'$.root'),''),m.detail
 FROM session_moves m LEFT JOIN hosts h ON h.host_id=m.peer_host
 WHERE m.role='out' AND m.state IN ('preparing','sent')`

func unfinishedMovesOut(query rowQuerier, where string, args ...any) (map[string]MoveShown, error) {
	rows, err := query.Query(moveShownQuery+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shown := map[string]MoveShown{}
	for rows.Next() {
		var conversation, state string
		var unknown bool
		var move MoveShown
		if err := rows.Scan(&conversation, &move.ID, &state, &unknown, &move.PeerHost, &move.HostName, &move.Root, &move.Detail); err != nil {
			return nil, err
		}
		switch {
		case state == MovePreparing:
			move.State = MoveShownHolding
		case unknown:
			move.State = MoveShownSettling
		default:
			move.State = MoveShownCopying
		}
		shown[conversation] = move
	}
	return shown, rows.Err()
}

func (r *Registry) UnfinishedMovesOut() (map[string]MoveShown, error) {
	return unfinishedMovesOut(r.read, "")
}

func (r *Registry) UnfinishedMoveOut(conversation string) (*MoveShown, error) {
	shown, err := unfinishedMovesOut(r.read, ` AND m.conversation=?`, conversation)
	if err != nil {
		return nil, err
	}
	if move, ok := shown[conversation]; ok {
		return &move, nil
	}
	return nil, nil
}

// MarkMoveOutcomeUnknown keeps the first time a sent move's target could not
// be asked; setMoveStateTx clears it on any change of state.
func (r *Registry) MarkMoveOutcomeUnknown(id, detail string) error {
	_, err := r.db.Exec(`UPDATE session_moves SET outcome_unknown_at=CASE WHEN outcome_unknown_at='' THEN ? ELSE outcome_unknown_at END,detail=? WHERE id=? AND role='out' AND state=?`, formatTimestamp(r.now()), detail, id, MoveSent)
	return err
}
