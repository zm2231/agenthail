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
	KeepsBreadth   bool         `json:"keepsBreadth,omitempty"`
}

var outwardWords = regexp.MustCompile(`(?i)\b(send\w*|sent|e-?mail\w*|ship\w*|merg\w*|deploy\w*|push\w*|force-push\w*|publish\w*|releas\w*|launch\w*|go(es|ing)? live|roll(s|ed|ing)? ?out|spen[dt]\w*|pa(y|ys|ying|id|yment|yments)|purchas\w*|buy\w*|bought|charg\w*|refund\w*|transfer\w*|delet\w*|destroy\w*|wip(e|es|ed|ing)|truncat\w*|drop table|rm|uninstall\w*|terminat\w*|revok\w*|upload\w*|submit\w*|post(s|ed|ing)?|tweet\w*|announc\w*|invit\w*|notify\w*|contact\w*|sign(s|ed|ing)? up)\b`)

var narrowingWords = regexp.MustCompile(`(?i)\b(drop\w*|defer\w*|skip\w*|later|postpon\w*|smaller|minimal\w*|minimum|only|without|exclud\w*|cut\w*|omit\w*|leav(e|es|ing) out|left out|not now|remov\w*|limit\w*|restrict\w*|narrow\w*|reduc\w*|simplif\w*|fewer|less|subset|partial\w*|stub\w*|mvp|instead|descop\w*|de-scop\w*|punt\w*|shelv\w*|backlog\w*|out of scope|trim\w*|strip\w*|lighter|cheaper|quick(er)? version)\b`)

func (s *Store) AskQuestion(req QuestionRequest, now time.Time) (Question, error) {
	q, err := req.question(now)
	if err != nil {
		return Question{}, err
	}
	board, release, err := s.lockBoard(req.ProjectKey)
	if err != nil {
		return Question{}, err
	}
	defer release()
	tx, err := s.begin(req.ProjectKey)
	if err != nil {
		return Question{}, err
	}
	defer tx.Rollback()
	if q.GoalID, err = board.targetGoal(req.To); err != nil {
		return Question{}, err
	}
	if q.ID, err = (&idIssuer{q: tx, projectKey: req.ProjectKey}).next(EventQuestion, "Q"); err != nil {
		return Question{}, err
	}
	data, err := json.Marshal(QuestionPayload{
		QuestionID: q.ID, Target: q.Target, GoalID: q.GoalID, Kind: q.Kind, Text: q.Text, Recommendation: q.Recommendation,
		DefaultAnswer: q.DefaultAnswer, DeadlineMs: q.DeadlineMs, KeepsBreadth: q.KeepsBreadth, OwnerSessionID: q.OwnerSessionID,
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
	q := Question{Target: req.To, Kind: req.Kind, Text: text, Recommendation: rec, DefaultAnswer: def, DeadlineMs: req.DeadlineMs, KeepsBreadth: req.KeepsBreadth, OwnerSessionID: req.SessionID, State: QuestionOpen}
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
	if req.KeepsBreadth && narrowingWords.MatchString(def) {
		return Question{}, refuse("the default reads as narrowing (%q), so it cannot be declared --keeps-breadth; without it the default waits for the person's ack", narrowingWords.FindString(def))
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
