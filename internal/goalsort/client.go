package goalsort

import (
	"context"
	"net/http"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/systemone"
)

const (
	ConfigKey = "ledgerSorter"

	ChoiceNew  = "new"
	ChoiceNone = "none"

	messageChars = 6000
	replyChars   = 2000
)

const instructions = "Which project goal is the person's message (`message`) about? Use `agent_previous_reply` to resolve what the message refers to. Each goal lists requests already filed under it."

type Goal struct {
	ID       string
	Name     string
	Requests []string
}

type Question struct {
	Message       string
	PreviousReply string
	Goals         []Goal
}

type Answer struct {
	Choice     string
	Confidence float64
}

type Client struct {
	Config systemone.Config
	HTTP   *http.Client
}

type criterion struct {
	Goal             string   `json:"goal"`
	ExistingRequests []string `json:"existing_requests"`
}

type state struct {
	Message            string `json:"message"`
	AgentPreviousReply string `json:"agent_previous_reply"`
}

func parts(q Question) (state, map[string]systemone.Question) {
	criteria := map[string]any{
		ChoiceNew:  "a new topic not covered by any goal above",
		ChoiceNone: "pure thanks or acknowledgement with no topic",
	}
	for _, g := range q.Goals {
		criteria[g.ID] = criterion{Goal: g.Name, ExistingRequests: append([]string{}, g.Requests...)}
	}
	return state{Message: head(q.Message, messageChars), AgentPreviousReply: tail(q.PreviousReply, replyChars)},
		map[string]systemone.Question{"goal": {Type: systemone.TypeChoice, Instructions: instructions, Criteria: criteria}}
}

func Body(model string, q Question) ([]byte, error) {
	s, questions := parts(q)
	return systemone.Body(model, s, questions)
}

func (c Client) Ask(ctx context.Context, q Question) (Answer, error) {
	s, questions := parts(q)
	answers, err := systemone.Client{Config: c.Config, HTTP: c.HTTP}.Ask(ctx, s, questions)
	if err != nil {
		return Answer{}, err
	}
	goal := answers["goal"]
	return Answer{Choice: goal.Choice, Confidence: goal.Confidence}, nil
}

func head(text string, chars int) string {
	if utf8.RuneCountInString(text) <= chars {
		return text
	}
	return string([]rune(text)[:chars])
}

func tail(text string, chars int) string {
	runes := []rune(text)
	return string(runes[max(0, len(runes)-chars):])
}
