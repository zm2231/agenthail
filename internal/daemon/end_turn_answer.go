package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const turnEndRequestPrefix = "turn-end-"

func turnEndRequestID(startedAt time.Time) string {
	return turnEndRequestPrefix + strconv.FormatInt(startedAt.UnixMilli(), 10)
}

func isTurnEndRequest(requestID string) bool {
	return strings.HasPrefix(requestID, turnEndRequestPrefix)
}

func endAsksPerson(end registry.TurnEnd) bool {
	return end.Needs == endTurnNeedsYou && end.RoutedTo == "" && end.AnsweredAt.IsZero() && len(end.Asks) > 0
}

func turnEndRequest(end registry.TurnEnd) surface.Request {
	questions := make([]surface.Question, 0, len(end.Asks))
	for _, ask := range end.Asks {
		question := surface.Question{Question: ask.Question, Options: make([]surface.QuestionOption, 0, len(ask.Options)), Suggested: ask.Suggested}
		for _, option := range ask.Options {
			question.Options = append(question.Options, surface.QuestionOption{Label: option.Label, Description: option.Does})
		}
		for _, link := range ask.Links {
			question.Links = append(question.Links, surface.QuestionLink{Title: link.Title, URL: link.URL})
		}
		questions = append(questions, question)
	}
	return surface.Request{ID: turnEndRequestID(end.StartedAt), Kind: surface.WaitingAsk, Title: end.Headline, Questions: questions, CreatedAt: end.EndedAt}
}

func (d *Daemon) openTurnEndRequest(sessionID string) (registry.TurnEnd, surface.Request, bool, error) {
	end, found, err := d.Registry.CurrentTurnEnd(sessionID)
	if err != nil || !found || !endAsksPerson(end) {
		return registry.TurnEnd{}, surface.Request{}, false, err
	}
	return end, turnEndRequest(end), true, nil
}

// composeTurnAnswer is the one message that answers an ended turn's asks:
// a numbered line per answered ask, in the order asked, then the note.
func composeTurnAnswer(questions []surface.Question, decision surface.Decision) string {
	var lines []string
	for index, question := range questions {
		if answer := strings.TrimSpace(decision.Answers[question.Question]); answer != "" {
			lines = append(lines, fmt.Sprintf("%d. %s %s", index+1, question.Question, answer))
		}
	}
	if note := strings.TrimSpace(decision.Message); note != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, note)
	}
	return strings.Join(lines, "\n")
}

// answerTurnEnd sends the person's answer to an ended turn's asks as their
// next message, exactly once: the ask is claimed before delivery and released
// only when delivery provably did not happen. Any other failure keeps it
// answered, since the message may have arrived.
func (d *Daemon) answerTurnEnd(ctx context.Context, adapter surface.Surface, session *surface.Session, requestID string, decision surface.Decision, personName, actor string) (actionResult, error) {
	end, request, open, err := d.openTurnEndRequest(session.ID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !open || request.ID != requestID {
		return actionResult{}, staleRequest()
	}
	if decision.Choice != "" {
		if decision, err = request.Choose(decision.Choice, decision.Message); err != nil {
			return actionResult{}, invalidRequest(err.Error())
		}
	}
	message := composeTurnAnswer(request.Questions, decision)
	if message == "" {
		return actionResult{}, invalidRequest("answer at least one question or write a note")
	}
	claimed, err := d.Registry.ClaimTurnAnswer(session.ID, end.StartedAt, time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	options := surface.SendOptions{Author: surface.AuthorPerson, PersonName: personName}
	receipt, deliveryErr := coordinator.Sender{Service: d.coordinator()}.DeliverWithOptions(ctx, adapter, session, message, "", options)
	defer d.publishTurnAnswer(session.ID)
	if deliveryErr != nil {
		if surface.IsDeliveryTerminal(deliveryErr) || surface.IsDeliveryUnavailable(deliveryErr) {
			if err := d.Registry.ReleaseTurnAnswer(session.ID, end.StartedAt); err != nil {
				d.log.Printf("release the answer to %s: %s", d.resolveDisplay(session.ID), err)
			}
			return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", deliveryErr.Error())
		}
		if err := d.Registry.RecordTurnAnswer(session.ID, end.StartedAt, string(surface.EvidenceUnknown), ""); err != nil {
			d.log.Printf("record the answer to %s: %s", d.resolveDisplay(session.ID), err)
		}
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", "the answer may have reached the session, so it is not sent again: "+deliveryErr.Error())
	}
	if err := d.Registry.RecordTurnAnswer(session.ID, end.StartedAt, receipt.Status, receipt.MessageID); err != nil {
		d.log.Printf("record the answer to %s: %s", d.resolveDisplay(session.ID), err)
	}
	entry := registry.HistoryEntry{Kind: "request-answered", SessionID: session.ID, Message: requestID, Result: "answered by " + actor}
	if err := d.Registry.RecordHistory(entry); err != nil {
		d.log.Printf("record the answer to %s: %s", d.resolveDisplay(session.ID), err)
	}
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": requestID, "outcome": "answered", "receipt": receipt}}), nil
}

func (d *Daemon) publishTurnAnswer(sessionID string) {
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", sessionID, map[string]string{"source": "ask-answered"})
}
