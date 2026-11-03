package surface

import "context"

type leadStartContextKey struct{}

func WithLeadStart(ctx context.Context) context.Context {
	return context.WithValue(ctx, leadStartContextKey{}, true)
}

func IsLeadStart(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	lead, _ := ctx.Value(leadStartContextKey{}).(bool)
	return lead
}
