package daemon

import (
	"context"
	"errors"
	"strings"
)

type mcpAskInput struct {
	Title               string             `json:"title,omitempty" jsonschema:"One line, at most 80 characters: what you need decided."`
	Questions           []askQuestionInput `json:"questions,omitempty" jsonschema:"1 to 4. Limits: question 160 characters, 1 to 6 options, label 40, does 90; recommended is an option index; at most 3 links."`
	Withdraw            string             `json:"withdraw,omitempty" jsonschema:"The id of an open ask of yours to withdraw instead."`
	Item                string             `json:"item,omitempty" jsonschema:"The work item it is about; default your one in progress."`
	Default             *int               `json:"default,omitempty" jsonschema:"One question only: the option index taken if unanswered; only an option safe without the person."`
	DefaultAfterMinutes int                `json:"defaultAfterMinutes,omitempty" jsonschema:"Minutes until default applies, 15 to 10080."`
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
	ask, note, err := d.raiseAsk(caller.session, askInput{Title: input.Title, Questions: input.Questions, Item: input.Item, Default: input.Default, DefaultAfterMinutes: input.DefaultAfterMinutes})
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
	reply := map[string]any{"ok": true, "askId": ask.ID, "routedTo": routedTo}
	if ask.WorkItem != "" {
		reply["workItem"] = ask.WorkItem
	}
	if note != "" {
		reply["note"] = note
	}
	if _, defaulted := askDefault(ask); defaulted {
		reply["defaultAt"] = ask.Deadline
	}
	return reply, nil
}
