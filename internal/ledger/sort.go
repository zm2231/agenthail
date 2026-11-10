package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const SortConfidence = 0.9

const sortRequests = 12

type SortedPayload struct {
	UtteranceID string  `json:"utteranceId"`
	GoalID      string  `json:"goalId"`
	Confidence  float64 `json:"confidence"`
	Model       string  `json:"model"`
	Ms          int64   `json:"ms"`
}

type Sorted struct {
	GoalID     string  `json:"goalId"`
	Confidence float64 `json:"confidence"`
	Overridden bool    `json:"overridden,omitempty"`
}

type SortStats struct {
	Filed      int `json:"filed"`
	Overridden int `json:"overridden"`
}

type SortGoal struct {
	ID       string
	Name     string
	Requests []string
}

type SortInput struct {
	UtteranceID string
	TargetID    string
	Text        string
	At          time.Time
	Goals       []SortGoal
}

type OwedSort struct {
	Ref string
	Seq int64
}

func sortedRef(utteranceID string) string { return "sorted:" + utteranceID }

func (s *Store) RecordSort(projectKey string, p SortedPayload, now time.Time) (bool, error) {
	if p.Confidence < SortConfidence {
		return false, refuse("a sort below confidence %.2f files nothing", SortConfidence)
	}
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return false, err
	}
	defer release()
	if !board.sortable(p.UtteranceID, p.GoalID) {
		return false, nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	tx, err := s.begin(projectKey)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	e, err := insertEvent(tx, `INSERT OR IGNORE`, Event{ProjectKey: projectKey, Type: EventSorted, Payload: data, Ref: sortedRef(p.UtteranceID), CreatedAt: now.UTC()})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return e.Seq != 0, s.catchUp(board, projectKey)
}

func (s *Store) SortInputFor(projectKey, utteranceID string) (SortInput, bool, error) {
	current, err := s.Board(projectKey)
	if err != nil {
		return SortInput{}, false, err
	}
	if !current.provisional(utteranceID) {
		return SortInput{}, false, nil
	}
	events, err := loadEvents(s.read, projectKey)
	if err != nil {
		return SortInput{}, false, err
	}
	at := slices.IndexFunc(events, func(e Event) bool { return e.Type == EventUtterance && e.Ref == utteranceID })
	if at < 0 {
		return SortInput{}, false, nil
	}
	then := NewBoard()
	if err := then.Rebuild(events[:at+1]); err != nil {
		return SortInput{}, false, err
	}
	then.mu.RLock()
	defer then.mu.RUnlock()
	u := then.utterances[utteranceID]
	if u == nil || len(then.goals) == 0 {
		return SortInput{}, false, nil
	}
	spoken, _ := time.Parse(time.RFC3339, u.At)
	return SortInput{UtteranceID: utteranceID, TargetID: u.TargetID, Text: u.Text, At: spoken, Goals: then.sortGoals(events[:at], utteranceID)}, true, nil
}

// Only this host's captures: two hosts sorting one message would collide on its ref when they sync.
func (s *Store) LocalProvisionalFrom(projectKey string, fromSeq int64, notBefore time.Time) ([]OwedSort, error) {
	if whole := notBefore.Truncate(time.Second); whole.Before(notBefore) {
		notBefore = whole.Add(time.Second)
	}
	rows, err := s.read.Query(`SELECT ref, seq FROM ledger_events WHERE project_key=? AND type=? AND seq>=? AND actor_host_id=COALESCE((SELECT host_id FROM host_identity WHERE id=1),'')
		AND json_extract(payload,'$.ts')>=? ORDER BY seq`, projectKey, string(EventUtterance), fromSeq, notBefore.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	var owed []OwedSort
	for rows.Next() {
		var o OwedSort
		if err := rows.Scan(&o.Ref, &o.Seq); err != nil {
			rows.Close()
			return nil, err
		}
		owed = append(owed, o)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	board, err := s.Board(projectKey)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(owed, func(o OwedSort) bool { return !board.provisional(o.Ref) }), rows.Err()
}

func (s *Store) LocalCaptureProjects(notBefore time.Time) ([]string, error) {
	rows, err := s.read.Query(`SELECT DISTINCT project_key FROM ledger_events WHERE type=? AND actor_host_id=COALESCE((SELECT host_id FROM host_identity WHERE id=1),'')
		AND json_extract(payload,'$.ts')>=? ORDER BY project_key`, string(EventUtterance), notBefore.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (b *Board) provisional(utteranceID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	u := b.utterances[utteranceID]
	_, retracted := b.retracted[utteranceID]
	return u != nil && !retracted && !u.Attached && u.Sorted == nil
}

func (b *Board) sortable(utteranceID, goalID string) bool {
	if !b.provisional(utteranceID) {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.goals[goalID] != nil
}

func (b *Board) sortGoals(before []Event, utteranceID string) []SortGoal {
	requests := map[string][]string{}
	for _, e := range before {
		if e.Type != EventClause {
			continue
		}
		var p ClausePayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		c := b.clauses[p.ClauseID]
		if c == nil || slices.Contains(c.Sources, utteranceID) {
			continue
		}
		if goal := b.clauseGoal(c); goal != "" {
			requests[goal] = append(requests[goal], c.Text)
		}
	}
	goals := make([]SortGoal, 0, len(b.goals))
	for id, g := range b.goals {
		texts := requests[id]
		goals = append(goals, SortGoal{ID: id, Name: g.Name, Requests: slices.Clone(texts[max(0, len(texts)-sortRequests):])})
	}
	slices.SortFunc(goals, func(a, b SortGoal) int { return compareGoalIDs(a.ID, b.ID) })
	return goals
}

func compareGoalIDs(a, b string) int {
	an, aok := goalNumber(a)
	bn, bok := goalNumber(b)
	if aok && bok && an != bn {
		return an - bn
	}
	return strings.Compare(a, b)
}

func goalNumber(id string) (int, bool) {
	i := strings.LastIndex(id, "-G")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(id[i+2:])
	return n, err == nil
}

func (b *Board) applySorted(data []byte) error {
	var p SortedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	u := b.utterances[p.UtteranceID]
	if u == nil || u.Attached || u.Sorted != nil || b.goals[p.GoalID] == nil {
		return nil
	}
	u.Sorted = &Sorted{GoalID: p.GoalID, Confidence: p.Confidence}
	return nil
}

func (b *Board) SortStats() SortStats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var stats SortStats
	for _, u := range b.utterances {
		if u.Sorted == nil {
			continue
		}
		stats.Filed++
		if u.Sorted.Overridden {
			stats.Overridden++
		}
	}
	return stats
}

func (s SortStats) String() string {
	if s.Filed == 0 {
		return ""
	}
	return fmt.Sprintf("auto-filed %d, overridden %d", s.Filed, s.Overridden)
}
