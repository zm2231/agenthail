package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// AskOriginQuestion is a question the session's own tool put to the person,
// answered by a message the provider reads as the answer.
const AskOriginQuestion = "question"

type QuestionSync struct {
	Opened []Ask
	Closed []Ask
}

// QuestionAskNeedsRoute reports whether open holds a question no ask was
// ever raised for, the only case SyncQuestionAsks routes.
func (r *Registry) QuestionAskNeedsRoute(sessionID string, open []surface.ProviderQuestion) (bool, error) {
	known, err := r.questionAskRefs(context.Background(), r.read, sessionID)
	if err != nil {
		return false, err
	}
	for _, question := range open {
		if _, found := known[question.Ref]; !found {
			return true, nil
		}
	}
	return false, nil
}

// SyncQuestionAsks raises one ask, routed to routedTo, for each question
// with no ask yet, and withdraws the open asks whose question was answered
// elsewhere. A question is raised once: an ask answered, withdrawn or
// dismissed is not raised again.
func (r *Registry) SyncQuestionAsks(sessionID string, found surface.ProviderQuestions, routedTo string, now time.Time) (QuestionSync, error) {
	var sync QuestionSync
	tx, err := r.db.Begin()
	if err != nil {
		return sync, err
	}
	defer tx.Rollback()
	ctx := context.Background()
	known, err := r.questionAskRefs(ctx, tx, sessionID)
	if err != nil {
		return sync, err
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	for _, ref := range found.Answered {
		ask, asked := known[ref]
		if !asked || ask.State != AskOpen {
			continue
		}
		if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, at.UnixMilli(), ask.ID, AskOpen); err != nil {
			return sync, err
		}
		ask.State, ask.AnsweredAt = AskWithdrawn, at
		sync.Closed = append(sync.Closed, ask)
	}
	for _, question := range found.Open {
		if _, asked := known[question.Ref]; asked || routedTo == "" {
			continue
		}
		ask, err := raiseQuestionAsk(tx, sessionID, question, routedTo, at)
		if err != nil {
			return sync, err
		}
		known[question.Ref] = ask
		sync.Opened = append(sync.Opened, ask)
	}
	return sync, tx.Commit()
}

func (r *Registry) questionAskRefs(ctx context.Context, q askQuerier, sessionID string) (map[string]Ask, error) {
	asks, err := queryAsks(ctx, q, `WHERE session_id=? AND origin=? ORDER BY created_at_ms, id`, sessionID, AskOriginQuestion)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]Ask, len(asks))
	for _, ask := range asks {
		byRef[ask.Ref] = ask
	}
	return byRef, nil
}

func raiseQuestionAsk(tx *sql.Tx, sessionID string, question surface.ProviderQuestion, routedTo string, at time.Time) (Ask, error) {
	id, err := newAskID()
	if err != nil {
		return Ask{}, err
	}
	ask := Ask{ID: id, SessionID: sessionID, Origin: AskOriginQuestion, Title: clip(question.Questions[0].Question, MaxAskTitle), Questions: questionAskQuestions(question.Questions),
		RoutedTo: routedTo, RoutedAt: at, Route: []AskHop{{To: routedTo, At: at, Why: AskRaised}}, State: AskOpen, CreatedAt: at, Ref: question.Ref, Answerable: true}
	questions, err := json.Marshal(ask.Questions)
	if err != nil {
		return Ask{}, err
	}
	route, err := json.Marshal(ask.Route)
	if err != nil {
		return Ask{}, err
	}
	_, err = tx.Exec(`INSERT INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,ref,answerable) VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`,
		ask.ID, ask.SessionID, ask.Origin, ask.Title, string(questions), ask.RoutedTo, at.UnixMilli(), string(route), AskOpen, at.UnixMilli(), ask.Ref)
	return ask, err
}

// questionAskQuestions keeps every option's text whole: the chosen label is
// the answer the provider receives.
func questionAskQuestions(asked []surface.Question) []AskQuestion {
	questions := make([]AskQuestion, 0, len(asked))
	for _, in := range asked {
		question := AskQuestion{Question: clip(in.Question, MaxAskQuestion)}
		if question.Question != in.Question {
			question.Detail = clip(in.Question, MaxAskDetail)
		}
		for i, option := range in.Options {
			question.Options = append(question.Options, AskOption{ID: fmt.Sprintf("o%d", i), Label: option.Label, Does: option.Description})
		}
		if in.Suggested != nil && *in.Suggested < len(question.Options) {
			question.Recommended = question.Options[*in.Suggested].ID
		}
		questions = append(questions, question)
	}
	return questions
}
