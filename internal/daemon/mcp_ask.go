package daemon

import (
	"context"
	"errors"
	"strings"
)

type mcpAskInput struct {
	Title     string             `json:"title,omitempty" jsonschema:"One line, at most 80 characters: what you need decided."`
	Questions []askQuestionInput `json:"questions,omitempty" jsonschema:"1 to 4. Limits: question 160 characters, 1 to 6 options, label 40, does 90; recommended is an option index; at most 3 links."`
	Withdraw  string             `json:"withdraw,omitempty" jsonschema:"The id of an open ask of yours to withdraw instead."`
}

func (d *Daemon) mcpAsk(_ context.Context, caller *mcpCaller, input mcpAskInput) (any, error) {
	if id := strings.TrimSpace(input.Withdraw); id != "" {
		switch err := d.withdrawOwnAsk(caller.session, id); {
		case errors.Is(err, errAskNotFound):
			return nil, refuse("not_found", err.Error()+".")
		case errors.Is(err, errAskSettled):
			return nil, &mcpRefusal{Code: "not_found", Message: err.Error() + ".", Detail: map[string]any{"stale": true}}
		case err != nil:
			return nil, err
		}
		return map[string]any{"ok": true, "askId": id, "outcome": "withdrawn"}, nil
	}
	ask, err := d.raiseAsk(caller.session, askInput{Title: input.Title, Questions: input.Questions})
	var invalid askInvalid
	if errors.As(err, &invalid) {
		return nil, refuse("invalid", err.Error()+".")
	}
	if err != nil {
		return nil, err
	}
	routedTo := ask.RoutedTo
	if routedTo != "person" {
		routedTo = d.answerTarget(routedTo)
	}
	return map[string]any{"ok": true, "askId": ask.ID, "routedTo": routedTo}, nil
}
