package surface

import "context"

type ModelChange struct {
	Model string `json:"model"`
}

func (c ModelChange) Text() string { return c.Model }

type ModelSetter interface {
	SetModel(ctx context.Context, sess *Session, name string) (ModelChange, error)
}
