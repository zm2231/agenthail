package surface

import (
	"context"
	"fmt"
)

// ModelChange is the outcome of choosing a model. PendingModel is set when
// the runtime could not switch now and the choice reaches the conversation
// with the next turn Agenthail sends; Model is then the model it still runs.
type ModelChange struct {
	Model        string `json:"model"`
	PendingModel string `json:"pendingModel,omitempty"`
	Limit        string `json:"limit,omitempty"`
}

func (c ModelChange) Text() string {
	if c.PendingModel == "" {
		return c.Model
	}
	text := fmt.Sprintf("%s applies from the next turn sent through Agenthail; the conversation runs %s until then.", c.PendingModel, c.Model)
	if c.Limit != "" {
		text += " " + c.Limit
	}
	return text
}

type ModelSetter interface {
	SetModel(ctx context.Context, sess *Session, name string) (ModelChange, error)
}

type PendingModelReader interface {
	PendingModel(sessionID string) string
}
