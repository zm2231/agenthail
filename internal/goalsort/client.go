package goalsort

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	ChoiceNew  = "new"
	ChoiceNone = "none"

	messageChars = 6000
	replyChars   = 2000
	answerBytes  = 64 * 1024
)

const instructions = "Which project goal is the person's message (`message`) about? Use `agent_previous_reply` to resolve what the message refers to. Each goal lists requests already filed under it."

type Config struct {
	URL   string `json:"url"`
	Model string `json:"model"`
}

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
	Choice     string  `json:"choice"`
	Confidence float64 `json:"confidence"`
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

type criterion struct {
	Goal             string   `json:"goal"`
	ExistingRequests []string `json:"existing_requests"`
}

type request struct {
	Model     string            `json:"model"`
	State     state             `json:"state"`
	Questions map[string]choice `json:"questions"`
}

type state struct {
	Message            string `json:"message"`
	AgentPreviousReply string `json:"agent_previous_reply"`
}

type choice struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria"`
}

func Body(model string, q Question) ([]byte, error) {
	criteria := map[string]any{
		ChoiceNew:  "a new topic not covered by any goal above",
		ChoiceNone: "pure thanks or acknowledgement with no topic",
	}
	for _, g := range q.Goals {
		criteria[g.ID] = criterion{Goal: g.Name, ExistingRequests: append([]string{}, g.Requests...)}
	}
	return json.Marshal(request{
		Model:     model,
		State:     state{Message: head(q.Message, messageChars), AgentPreviousReply: tail(q.PreviousReply, replyChars)},
		Questions: map[string]choice{"goal": {Type: "choice", Instructions: instructions, Criteria: criteria}},
	})
}

func (c Client) Ask(ctx context.Context, q Question) (Answer, error) {
	body, err := Body(c.Config.Model, q)
	if err != nil {
		return Answer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Config.URL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return Answer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Answer{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, answerBytes))
	if err != nil {
		return Answer{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Answer{}, fmt.Errorf("the sorter answered %s", resp.Status)
	}
	var parsed struct {
		Answers struct {
			Goal *Answer `json:"goal"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Answer{}, fmt.Errorf("the sorter's answer is not JSON: %w", err)
	}
	a := parsed.Answers.Goal
	if a == nil || a.Choice == "" {
		return Answer{}, errors.New("the sorter's answer has no goal choice")
	}
	if a.Confidence < 0 || a.Confidence > 1 {
		return Answer{}, fmt.Errorf("the sorter's confidence %v is outside 0 to 1", a.Confidence)
	}
	return *a, nil
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
