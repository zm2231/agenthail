package registry

import "context"

type bypassReadCaches struct{}

func WithoutReadCaches(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassReadCaches{}, true)
}

func readCachesBypassed(ctx context.Context) bool {
	bypassed, _ := ctx.Value(bypassReadCaches{}).(bool)
	return bypassed
}
