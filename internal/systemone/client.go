package systemone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	TypeNoul   = "noul"
	TypeChoice = "choice"

	answerBytes = 64 * 1024
)

type Config struct {
	URL   string `json:"url"`
	Model string `json:"model"`
}

type Question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria,omitempty"`
}

type Answer struct {
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

type request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

func Body(model string, state any, questions map[string]Question) ([]byte, error) {
	return json.Marshal(request{Model: model, State: state, Questions: questions})
}

func (c Client) Ask(ctx context.Context, state any, questions map[string]Question) (map[string]Answer, error) {
	body, err := Body(c.Config.Model, state, questions)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Config.URL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, answerBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the model answered %s", resp.Status)
	}
	var parsed struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("the model's answer is not JSON: %w", err)
	}
	answers := make(map[string]Answer, len(questions))
	for name, question := range questions {
		answer, err := readAnswer(name, question.Type, parsed.Answers[name])
		if err != nil {
			return nil, err
		}
		answers[name] = answer
	}
	return answers, nil
}

func readAnswer(name, kind string, raw json.RawMessage) (Answer, error) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return Answer{}, fmt.Errorf("the model gave no answer to %s", name)
	}
	var answer Answer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Answer{}, fmt.Errorf("the model's answer to %s is malformed: %w", name, err)
	}
	switch kind {
	case TypeNoul:
		if _, ok := fields["noul"]; !ok || !unit(answer.Noul) {
			return Answer{}, fmt.Errorf("the model's answer to %s has no probability between 0 and 1", name)
		}
	case TypeChoice:
		if answer.Choice == "" || !unit(answer.Confidence) {
			return Answer{}, fmt.Errorf("the model's answer to %s has no choice with a confidence between 0 and 1", name)
		}
		for choice, p := range answer.Probabilities {
			if !unit(p) {
				return Answer{}, fmt.Errorf("the model's probability of %s for %s is not between 0 and 1", choice, name)
			}
		}
	}
	return answer, nil
}

func unit(p float64) bool { return p >= 0 && p <= 1 }
