package surface

import "context"

type messageUUIDContextKey struct{}

// WithMessageUUID carries a message's delivery intent uuid to the surface
// that sends it, so a provider that keys messages by uuid records that one.
func WithMessageUUID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, messageUUIDContextKey{}, id)
}

func MessageUUID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(messageUUIDContextKey{}).(string)
	return id
}
