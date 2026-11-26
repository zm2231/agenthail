package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	ItemQuestionAnswered  = "answered"
	ItemQuestionWithdrawn = "withdrawn"
	ItemQuestionExpired   = "expired"
	ItemQuestionDefaulted = "defaulted"
)

var ErrItemGone = errors.New("the work item is gone")

var itemQuestionOutcomes = []string{ItemQuestionAnswered, ItemQuestionWithdrawn, ItemQuestionExpired, ItemQuestionDefaulted}

type ItemQuestionOption struct {
	Label string `json:"label"`
	Does  string `json:"does,omitempty"`
}

type ItemQuestionLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type ItemQuestionPrompt struct {
	Text        string               `json:"text"`
	Options     []ItemQuestionOption `json:"options,omitempty"`
	Recommended *int                 `json:"recommended,omitempty"`
	Links       []ItemQuestionLink   `json:"links,omitempty"`
}

type ItemQuestion struct {
	ID         string               `json:"id"`
	AskID      string               `json:"askId"`
	AskHostID  string               `json:"askHostId"`
	Title      string               `json:"title"`
	Prompts    []ItemQuestionPrompt `json:"prompts"`
	Default    *int                 `json:"defaultOption,omitempty"`
	DeadlineMs int64                `json:"deadlineMs,omitempty"`
	AskedBy    string               `json:"askedBy"`
	At         time.Time            `json:"at"`
	Outcome    string               `json:"outcome,omitempty"`
	Answer     string               `json:"answer,omitempty"`
	By         string               `json:"by,omitempty"`
	ResolvedAt time.Time            `json:"resolvedAt,omitzero"`
}

func (q ItemQuestion) Open() bool { return q.Outcome == "" }

type WorkQuestionPayload struct {
	QuestionID string               `json:"questionId"`
	ItemID     string               `json:"itemId"`
	AskID      string               `json:"askId"`
	AskHostID  string               `json:"askHostId"`
	Title      string               `json:"title"`
	Prompts    []ItemQuestionPrompt `json:"prompts"`
	Default    *int                 `json:"defaultOption,omitempty"`
	DeadlineMs int64                `json:"deadlineMs,omitempty"`
	Actor      string               `json:"actor"`
}

type WorkQuestionResolvedPayload struct {
	QuestionID string `json:"questionId"`
	Answer     string `json:"answer,omitempty"`
	By         string `json:"by"`
	Outcome    string `json:"outcome"`
}

type ItemQuestionRaise struct {
	ProjectKey string               `json:"projectKey"`
	SessionID  string               `json:"sessionId"`
	ItemID     string               `json:"itemId"`
	AskHostID  string               `json:"askHostId"`
	AskID      string               `json:"askId"`
	Title      string               `json:"title"`
	Prompts    []ItemQuestionPrompt `json:"prompts"`
	Default    *int                 `json:"defaultOption,omitempty"`
	DeadlineMs int64                `json:"deadlineMs,omitempty"`
}

type ItemQuestionResolve struct {
	ProjectKey string `json:"projectKey"`
	SessionID  string `json:"sessionId"`
	AskHostID  string `json:"askHostId"`
	AskID      string `json:"askId"`
	Answer     string `json:"answer,omitempty"`
	By         string `json:"by"`
	Outcome    string `json:"outcome"`
}

func itemQuestionRef(hostID, askID string) string { return "item-question:" + hostID + ":" + askID }

func itemQuestionResolvedRef(hostID, askID string) string {
	return "item-question-resolved:" + hostID + ":" + askID
}

// RaiseItemQuestion records an ask on the work item it is about and returns the question's id. Every try for one ask carries the
// same ref, so a retry after a lost reply returns the id issued the first time instead of a second question.
func (s *Store) RaiseItemQuestion(req ItemQuestionRaise, now time.Time) (string, error) {
	if req.AskHostID == "" || req.AskID == "" || strings.TrimSpace(req.Title) == "" || len(req.Prompts) == 0 {
		return "", refuse("an item question needs the ask's host and id, its title and its questions")
	}
	if (req.Default == nil) != (req.DeadlineMs == 0) || req.Default != nil && (len(req.Prompts) != 1 || *req.Default < 0 || *req.Default >= len(req.Prompts[0].Options)) {
		return "", refuse("an item question's default is an option of its only question, with a deadline")
	}
	board, release, err := s.lockBoard(req.ProjectKey)
	if err != nil {
		return "", err
	}
	defer release()
	tx, err := s.begin(req.ProjectKey)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := plannedHere(tx, req.ProjectKey); err != nil {
		return "", err
	}
	ref := itemQuestionRef(req.AskHostID, req.AskID)
	var held string
	switch err := tx.QueryRow(`SELECT payload FROM ledger_events WHERE project_key=? AND ref=?`, req.ProjectKey, ref).Scan(&held); {
	case err == nil:
		var payload WorkQuestionPayload
		if err := json.Unmarshal([]byte(held), &payload); err != nil {
			return "", err
		}
		return payload.QuestionID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	itemID, err := board.questionItem(req.ItemID, req.ProjectKey)
	if err != nil {
		return "", err
	}
	id, err := (&idIssuer{q: tx, projectKey: req.ProjectKey}).next(EventWorkQuestion, "IQ")
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(WorkQuestionPayload{QuestionID: id, ItemID: itemID, AskID: req.AskID, AskHostID: req.AskHostID, Title: oneLine(req.Title), Prompts: req.Prompts, Default: req.Default, DeadlineMs: req.DeadlineMs, Actor: req.SessionID})
	if err != nil {
		return "", err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: req.ProjectKey, Type: EventWorkQuestion, Payload: data, ActorSessionID: req.SessionID, Ref: ref, CreatedAt: now.UTC()}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, s.catchUp(board, req.ProjectKey)
}

func (b *Board) questionItem(itemID, projectKey string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	item := b.workItems[itemID]
	if item == nil || item.State == WorkDeleted {
		return "", fmt.Errorf("%w: %q is not a work item in %s", ErrItemGone, itemID, projectKey)
	}
	return item.ID, nil
}

// ResolveItemQuestion closes the question of an ask once the ask is answered, withdrawn or expired. A repeat is a no-op.
func (s *Store) ResolveItemQuestion(req ItemQuestionResolve, now time.Time) error {
	if !slices.Contains(itemQuestionOutcomes, req.Outcome) || req.By == "" {
		return refuse("an item question resolves as answered, withdrawn, expired or defaulted, by the person or a session")
	}
	board, release, err := s.lockBoard(req.ProjectKey)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.begin(req.ProjectKey)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := plannedHere(tx, req.ProjectKey); err != nil {
		return err
	}
	ref := itemQuestionResolvedRef(req.AskHostID, req.AskID)
	var seen int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND ref=?`, req.ProjectKey, ref).Scan(&seen); err != nil || seen > 0 {
		return err
	}
	question, found := board.ItemQuestionOfAsk(req.AskHostID, req.AskID)
	if !found {
		return refuse("no item question in %s carries ask %s", req.ProjectKey, req.AskID)
	}
	data, err := json.Marshal(WorkQuestionResolvedPayload{QuestionID: question.ID, Answer: req.Answer, By: req.By, Outcome: req.Outcome})
	if err != nil {
		return err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: req.ProjectKey, Type: EventWorkQuestionRes, Payload: data, ActorSessionID: req.SessionID, Ref: ref, CreatedAt: now.UTC()}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.catchUp(board, req.ProjectKey)
}

func (b *Board) ItemQuestionOfAsk(hostID, askID string) (ItemQuestion, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, item := range b.workItems {
		for _, q := range item.Questions {
			if q.AskHostID == hostID && q.AskID == askID {
				return q, true
			}
		}
	}
	return ItemQuestion{}, false
}

func (b *Board) applyWorkQuestion(event Event) error {
	var p WorkQuestionPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil || slices.ContainsFunc(item.Questions, func(q ItemQuestion) bool { return q.ID == p.QuestionID }) {
		return nil
	}
	item.Questions = append(item.Questions, ItemQuestion{ID: p.QuestionID, AskID: p.AskID, AskHostID: p.AskHostID, Title: p.Title, Prompts: p.Prompts, Default: p.Default, DeadlineMs: p.DeadlineMs, AskedBy: p.Actor, At: event.CreatedAt})
	return nil
}

func (b *Board) applyWorkQuestionResolved(event Event) error {
	var p WorkQuestionResolvedPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	for _, item := range b.workItems {
		for i := range item.Questions {
			if q := &item.Questions[i]; q.ID == p.QuestionID && q.Open() {
				q.Outcome, q.Answer, q.By, q.ResolvedAt = p.Outcome, p.Answer, p.By, event.CreatedAt
				return nil
			}
		}
	}
	return nil
}

func (item WorkItem) OpenQuestions() []ItemQuestion {
	var open []ItemQuestion
	for _, q := range item.Questions {
		if q.Open() {
			open = append(open, q)
		}
	}
	return open
}
