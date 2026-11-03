package surface

import (
	"context"
	"encoding/json"
)

type startCeilingContextKey struct{}

func WithStartCeiling(ctx context.Context, ceiling json.RawMessage) context.Context {
	return context.WithValue(ctx, startCeilingContextKey{}, ceiling)
}

func StartCeiling(ctx context.Context) json.RawMessage {
	if ctx == nil {
		return nil
	}
	ceiling, _ := ctx.Value(startCeilingContextKey{}).(json.RawMessage)
	return ceiling
}
