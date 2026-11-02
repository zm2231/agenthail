package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Elsewhere struct {
	ProjectKey string `json:"projectKey"`
	GoalID     string `json:"goalId,omitempty"`
}

func (e Elsewhere) String() string {
	if e.GoalID == "" {
		return e.ProjectKey
	}
	return e.ProjectKey + " " + e.GoalID
}

func attachedElsewhere(q querier, projectKey, utteranceID string) (*Elsewhere, error) {
	var e Elsewhere
	err := q.QueryRow(`SELECT project_key, COALESCE(json_extract(payload,'$.goalId'),'') FROM ledger_events
		WHERE type=? AND project_key<>? AND json_extract(payload,'$.utteranceId')=? ORDER BY seq LIMIT 1`,
		string(EventAttach), projectKey, utteranceID).Scan(&e.ProjectKey, &e.GoalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func attachedElsewhereAll(q querier, projectKey string) (map[string]Elsewhere, error) {
	rows, err := q.Query(`SELECT a.project_key, json_extract(a.payload,'$.utteranceId'), COALESCE(json_extract(a.payload,'$.goalId'),'')
		FROM ledger_events a JOIN ledger_events u ON u.project_key=? AND u.type=? AND u.ref=json_extract(a.payload,'$.utteranceId')
		WHERE a.type=? AND a.project_key<>? ORDER BY a.seq`,
		projectKey, string(EventUtterance), string(EventAttach), projectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]Elsewhere{}
	for rows.Next() {
		var e Elsewhere
		var ref string
		if err := rows.Scan(&e.ProjectKey, &ref, &e.GoalID); err != nil {
			return nil, err
		}
		if _, seen := found[ref]; !seen {
			found[ref] = e
		}
	}
	return found, rows.Err()
}

type Attachment struct {
	Unattached []UtteranceView       `json:"unattached"`
	Elsewhere  map[string]Elsewhere `json:"attachedElsewhere,omitempty"`
}

func attachment(q querier, projectKey string, board *Board, targetID string) (Attachment, error) {
	elsewhere, err := attachedElsewhereAll(q, projectKey)
	if err != nil {
		return Attachment{}, err
	}
	out := Attachment{Elsewhere: map[string]Elsewhere{}}
	for _, u := range board.Utterances() {
		if u.Attached || (targetID != "" && u.TargetID != targetID) {
			continue
		}
		if e, ok := elsewhere[u.ID]; ok {
			out.Elsewhere[u.ID] = e
			continue
		}
		out.Unattached = append(out.Unattached, u)
	}
	return out, nil
}

func (p *attachPlan) readback(q querier, board *Board) (string, error) {
	target := p.req.SessionID
	if p.utterance != nil {
		target = p.utterance.TargetID
	}
	state, err := attachment(q, p.req.ProjectKey, board, target)
	if err != nil {
		return "", err
	}
	line := "intent ledger: " + p.summary
	if n := len(state.Unattached); n > 0 {
		var ids []string
		for _, u := range state.Unattached[:min(n, 3)] {
			ids = append(ids, u.ID)
		}
		more := ""
		if n > 3 {
			more = ", ..."
		}
		line += fmt.Sprintf("; %d unattached: %s%s", n, strings.Join(ids, ", "), more)
	}
	if open := openQuestionsOn(board, p.goalID); len(open) > 0 {
		line += "; open questions: " + strings.Join(open, ", ")
	}
	return line, nil
}

func openQuestionsOn(board *Board, goalID string) []string {
	var open []Question
	for _, q := range board.Questions() {
		if q.State == QuestionOpen && q.GoalID == goalID {
			open = append(open, q)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].DeadlineMs < open[j].DeadlineMs })
	ids := make([]string, 0, len(open))
	for _, q := range open {
		ids = append(ids, q.ID)
	}
	return ids
}
