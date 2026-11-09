package ledger

import (
	"encoding/json"
	"strings"
	"time"
)

func (s *Store) EnsureGoal(projectKey, name, sessionID string) (Goal, bool, error) {
	name = strings.TrimSpace(name)
	if projectKey == "" || name == "" {
		return Goal{}, false, refuse("project and goal name are required")
	}
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return Goal{}, false, err
	}
	defer release()
	tx, err := s.begin(projectKey)
	if err != nil {
		return Goal{}, false, err
	}
	defer tx.Rollback()
	if g := board.goalByName(name); g != nil {
		return *g, false, nil
	}
	id, err := (&idIssuer{q: tx, projectKey: projectKey}).next(EventGoal, "G")
	if err != nil {
		return Goal{}, false, err
	}
	data, err := json.Marshal(GoalPayload{GoalID: id, Name: name})
	if err != nil {
		return Goal{}, false, err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: EventGoal, Payload: data, ActorSessionID: sessionID, CreatedAt: time.Now().UTC()}); err != nil {
		return Goal{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Goal{}, false, err
	}
	return Goal{ID: id, Name: name}, true, nil
}

func (s *Store) Attachment(projectKey string, board *Board) (Attachment, error) {
	return attachment(s.read, projectKey, board, "")
}

func (s *Store) ProvisionalFor(projectKey, targetID string) ([]UtteranceView, error) {
	board, err := s.Board(projectKey)
	if err != nil {
		return nil, err
	}
	state, err := attachment(s.read, projectKey, board, targetID)
	return state.Provisional, err
}
