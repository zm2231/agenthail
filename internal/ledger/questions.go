package ledger

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

type QuestionRequest struct {
	ProjectKey     string       `json:"projectKey"`
	SessionID      string       `json:"sessionId"`
	To             string       `json:"to"`
	Text           string       `json:"text"`
	Recommendation string       `json:"recommendation"`
	DefaultAnswer  string       `json:"defaultAnswer"`
	DeadlineMs     int64        `json:"deadlineMs"`
	Kind           QuestionKind `json:"kind"`
	Narrows        bool         `json:"narrows,omitempty"`
}

var outwardWords = regexp.MustCompile(`(?i)\b(send|sends|sending|sent|email|emails|e-mail|merge|merges|merging|merged|deploy|deploys|deploying|deployed|push|pushes|pushing|publish|publishes|publishing|release|releases|releasing|spend|spends|spending|pay|pays|paying|purchase|buy|charge|delete|deletes|deleting|deleted|destroy|wipe|force-push|rm)\b`)

var narrowingWords = regexp.MustCompile(`(?i)\b(drop|drops|dropping|defer|defers|deferring|skip|skips|skipping|later|postpone|smaller|minimal|only|without|exclude|excluding|cut|cuts|instead of|leave out|not now)\b`)

func (s *Store) AskQuestion(req QuestionRequest, now time.Time) (Question, error) {
	q, err := req.question(now)
	if err != nil {
		return Question{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Question{}, err
	}
	defer tx.Rollback()
	board, err := loadBoard(tx, req.ProjectKey)
	if err != nil {
		return Question{}, err
	}
	if q.GoalID, err = board.targetGoal(req.To); err != nil {
		return Question{}, err
	}
	if q.ID, err = (&idIssuer{q: tx, projectKey: req.ProjectKey}).next(EventQuestion, "Q"); err != nil {
		return Question{}, err
	}
	data, err := json.Marshal(QuestionPayload{
		QuestionID: q.ID, Target: q.Target, GoalID: q.GoalID, Kind: q.Kind, Text: q.Text, Recommendation: q.Recommendation,
		DefaultAnswer: q.DefaultAnswer, DeadlineMs: q.DeadlineMs, Narrows: q.Narrows, OwnerSessionID: q.OwnerSessionID,
	})
	if err != nil {
		return Question{}, err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: req.ProjectKey, Type: EventQuestion, Payload: data, ActorSessionID: req.SessionID, CreatedAt: now.UTC()}); err != nil {
		return Question{}, err
	}
	return q, tx.Commit()
}

func (req QuestionRequest) question(now time.Time) (Question, error) {
	text, rec, def := strings.TrimSpace(req.Text), strings.TrimSpace(req.Recommendation), strings.TrimSpace(req.DefaultAnswer)
	switch {
	case req.ProjectKey == "" || req.SessionID == "":
		return Question{}, refuse("project and asking session are required")
	case text == "" || req.To == "":
		return Question{}, refuse("a question needs --to <goal|ask|clause|project> and its text")
	case rec == "":
		return Question{}, refuse("a question needs --recommendation: the answer you suggest")
	case req.DeadlineMs <= now.UnixMilli():
		return Question{}, refuse("a question needs a --deadline in the future")
	}
	q := Question{Target: req.To, Kind: req.Kind, Text: text, Recommendation: rec, DefaultAnswer: def, DeadlineMs: req.DeadlineMs, Narrows: req.Narrows, OwnerSessionID: req.SessionID, State: QuestionOpen}
	switch req.Kind {
	case QuestionOutward:
		if def != "" && !strings.EqualFold(def, OutwardDefault) {
			return Question{}, refuse("an outward question's default is always %q; the person must answer to act", OutwardDefault)
		}
		q.DefaultAnswer = OutwardDefault
		return q, nil
	case QuestionScope, QuestionFraming:
	default:
		return Question{}, refuse("unknown --kind %q; use scope, framing or outward", req.Kind)
	}
	if def == "" {
		return Question{}, refuse("a %s question needs --default: what happens if the person does not answer", req.Kind)
	}
	for _, field := range []string{text, rec, def} {
		if outwardWords.MatchString(field) {
			return Question{}, refuse("this reads as an outward act (%q); ask it with --kind outward", outwardWords.FindString(field))
		}
	}
	if !req.Narrows && narrowingWords.MatchString(def) {
		return Question{}, refuse("the default reads as narrowing (%q); pass --narrows so it waits for the person's ack instead of applying itself", narrowingWords.FindString(def))
	}
	return q, nil
}

func (b *Board) targetGoal(target string) (string, error) {
	switch {
	case target == ProjectTarget:
		return "", nil
	case b.goals[target] != nil:
		return target, nil
	case b.asks[target] != nil:
		return b.asks[target].GoalID, nil
	case b.clauses[target] != nil:
		if b.clauses[target].State == StateRejected {
			return "", refuse("%s was rejected", target)
		}
		return b.clauseGoal(b.clauses[target]), nil
	}
	return "", refuse("%q is not a goal, ask or clause in this project", target)
}
