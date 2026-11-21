package transcript

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func ReadSessionReply(ctx context.Context, reader surface.SessionReader, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	read, err := reader.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: max(1, limit)})
	if err != nil {
		return nil, err
	}
	if read.Reply == nil {
		return &surface.ReplyResult{Done: false, Source: read.Source}, nil
	}
	return read.Reply, nil
}
