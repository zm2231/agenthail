package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) wakeLedgerAsks(key string) {
	d.ledgerAskMu.Lock()
	if d.ledgerAskDue[key] {
		d.ledgerAskMu.Unlock()
		return
	}
	if d.ledgerAskDue == nil {
		d.ledgerAskDue = map[string]bool{}
	}
	d.ledgerAskDue[key] = true
	d.ledgerAskMu.Unlock()
	done := func() {
		d.ledgerAskMu.Lock()
		delete(d.ledgerAskDue, key)
		d.ledgerAskMu.Unlock()
	}
	if !d.background.start(func(context.Context) {
		done()
		d.syncLedgerAsks(key)
	}) {
		done()
	}
}

func (d *Daemon) syncAllLedgerAsks() {
	keys, err := d.ledgerStore().ProjectsWith(ledger.EventQuestion)
	if err != nil {
		d.log.Printf("ledger asks: list projects: %s", err)
		return
	}
	for _, key := range keys {
		d.syncLedgerAsks(key)
	}
}

func (d *Daemon) syncLedgerAsks(key string) {
	d.ledgerAskSync.Lock()
	defer d.ledgerAskSync.Unlock()
	board, err := d.ledgerStore().Board(key)
	if err != nil {
		d.log.Printf("ledger asks: read %s: %s", key, err)
		return
	}
	byID := map[string]ledger.Question{}
	var questions []registry.LedgerQuestion
	for _, q := range board.Questions() {
		byID[q.ID] = q
		questions = append(questions, registry.LedgerQuestion{
			ID: q.ID, OwnerSessionID: q.OwnerSessionID, Text: q.Text, Recommendation: q.Recommendation, DefaultAnswer: q.DefaultAnswer,
			Deadline: time.UnixMilli(q.DeadlineMs), Answered: q.State == ledger.QuestionAnswered, Defaulted: q.State == ledger.QuestionDefaulted,
		})
	}
	closed, err := d.Registry.SyncLedgerAsks(key, questions, time.Now())
	if err != nil {
		d.log.Printf("ledger asks: sync %s: %s", key, err)
		return
	}
	for _, ask := range closed {
		d.withdrawAsk(ask.ID)
		if _, id, ok := registry.ParseLedgerAskRef(ask.Ref); ok && ask.State == registry.AskExpired {
			q := byID[id]
			d.tellLedgerOwner(ask, fmt.Sprintf("[intent ledger] %s (%q) had no answer by its deadline, so its default applied: %s", q.ID, q.Text, q.Answer), "ledger-ask-expired:"+ask.ID)
		}
		d.publishAsk(ask.SessionID)
	}
	d.announceLedgerAsks()
}

func (d *Daemon) announceLedgerAsks() {
	untold, err := d.Registry.UntoldLedgerAsks()
	if err != nil {
		d.log.Printf("ledger asks: read untold asks: %s", err)
		return
	}
	for _, ask := range untold {
		d.announceAsk(ask)
		if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
			d.log.Printf("ledger asks: mark %s told: %s", ask.ID, err)
		}
		d.publishAsk(ask.SessionID)
	}
}

func (d *Daemon) tellLedgerOwner(ask registry.Ask, message, key string) {
	if _, err := d.sendNotice(noticeRecipient{sessionID: ask.SessionID, role: "asker"}, message, key); err != nil {
		d.log.Printf("ledger asks: tell %s about %s: %s", d.resolveDisplay(ask.SessionID), ask.ID, err)
	}
}

func ledgerAnswerWords(ask registry.Ask, q ledger.Question, response surface.Response, decision surface.Decision) string {
	answer := strings.TrimSpace(decision.Answers[ask.Questions[0].Question])
	switch {
	case slices.Contains(response.Choices, registry.LedgerRecommended):
		answer = q.Recommendation
	case slices.Contains(response.Choices, registry.LedgerDefault):
		answer = q.DefaultAnswer
	}
	if note := strings.TrimSpace(decision.Message); note != "" {
		answer = strings.TrimSpace(answer + "\n\n" + note)
	}
	return answer
}

func (d *Daemon) answerLedger(ctx context.Context, ask registry.Ask, response surface.Response, p principal) (actionResult, error) {
	if p.Kind == principalSession {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person answers a ledger question; an agent's words are not the person's")
	}
	key, questionID, ok := registry.ParseLedgerAskRef(ask.Ref)
	if !ok || len(ask.Questions) != 1 {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", "unreadable ledger ask "+ask.ID)
	}
	decision, err := askRequest(ask).Resolve(response)
	if err != nil {
		return actionResult{}, invalidRequest(err.Error())
	}
	if decision.Behavior == "deny" {
		return actionResult{}, invalidRequest("answer the question; a ledger question cannot be declined")
	}
	board, err := d.ledgerStore().Board(key)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "ledger_error", err.Error())
	}
	q, found := ledgerQuestion(board, questionID)
	if !found || q.State != ledger.QuestionOpen {
		d.wakeLedgerAsks(key)
		return actionResult{}, staleRequest()
	}
	words := ledgerAnswerWords(ask, q, response, decision)
	if words == "" {
		return actionResult{}, invalidRequest("answer the question or write a note")
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: words}}, registry.AskToPerson, p.actor(), time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	defer d.publishAsk(ask.SessionID)
	ref, err := d.applyLedgerAnswer(ctx, ask, key, questionID, words)
	if err != nil {
		if releaseErr := d.Registry.ReleaseAsk(ask.ID); releaseErr != nil {
			d.log.Printf("reopen %s: %s", ask.ID, releaseErr)
		}
		return actionResult{}, err
	}
	if err := d.Registry.RecordAskApplied(ask.ID, "applied", ref, time.Now()); err != nil {
		d.log.Printf("record the answer to %s: %s", ask.ID, err)
	}
	d.withdrawAsk(ask.ID)
	d.tellLedgerOwner(ask, fmt.Sprintf("[intent ledger] The person answered %s (%q): %s", q.ID, q.Text, words), "ledger-ask-answered:"+ask.ID)
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "answered", "utteranceId": ref}}), nil
}

func ledgerQuestion(board *ledger.Board, id string) (ledger.Question, bool) {
	for _, q := range board.Questions() {
		if q.ID == id {
			return q, true
		}
	}
	return ledger.Question{}, false
}

func (d *Daemon) applyLedgerAnswer(ctx context.Context, ask registry.Ask, key, questionID, words string) (string, error) {
	session, err := d.Registry.Session(ask.SessionID)
	if err != nil {
		return "", actionFailed(http.StatusConflict, "conflict", err.Error())
	}
	ref := ledger.AskAnswerRef(ask.ID, words)
	utterance := ledger.Utterance{ProjectKey: key, Ref: ref, SpeakerID: surface.PersonSessionID, TargetID: session.ID, Surface: string(session.Surface), Text: words, At: time.Now(), CapturedBy: ledger.CapturedByAsk}
	if _, _, err := d.ledgerStore().RecordUtterance(utterance); err != nil {
		return "", actionFailed(http.StatusConflict, "conflict", err.Error())
	}
	body, err := json.Marshal(ledger.AttachRequest{ProjectKey: key, SessionID: session.ID, UtteranceID: ref, As: ledger.AsAnswers, To: questionID})
	if err != nil {
		return "", err
	}
	call, err := http.NewRequestWithContext(sessionPrincipal(session.ID).context(ctx), http.MethodPost, "/api/v1/ledger/attach", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	recorder := newActionRecorder()
	d.ledgerWriteRoute(d.apiLedgerAttachHandler)(recorder, call)
	reply := recorder.reply()
	if reply.Status < http.StatusMultipleChoices {
		return ref, nil
	}
	var failure struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(reply.Body, &failure)
	status, code := reply.Status, failure.Error.Code
	if status == http.StatusBadRequest {
		status, code = http.StatusConflict, "conflict"
	}
	return "", actionFailed(status, code, failure.Error.Message)
}
