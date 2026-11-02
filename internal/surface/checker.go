package surface

import "context"

type CheckerStart struct {
	Run     string
	Token   string
	Cwd     string
	Message string
}

type CheckerStarter interface {
	StartChecker(ctx context.Context, start CheckerStart) (*Session, error)
}
