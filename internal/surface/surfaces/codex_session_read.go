package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) readSessionFromRPC(ctx context.Context, sess *surface.Session, n int, recorded map[string]*surface.TurnMetadata) (*surface.SessionReadResult, error) {
	conn, err := c.openReader(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readThreadWithOptions(ctx, conn, sess.ID, n, true)
	if err != nil {
		return nil, err
	}

	var exchanges []surface.Exchange
	for _, turn := range thread.Turns {
		if turn.User != "" || turn.Assistant != "" {
			metadata := recorded[turn.ID]
			if metadata == nil {
				metadata = codexRPCTurnMetadata(turn)
			}
			exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant, Source: "rpc", Turn: metadata})
		}
	}

	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: exchanges, Reply: latestRPCReply(sess, exchanges, thread), Source: "rpc"}, nil
}

func latestRPCReply(session *surface.Session, exchanges []surface.Exchange, thread *codexThread) *surface.ReplyResult {
	for index := len(thread.Turns) - 1; index >= 0; index-- {
		turn := thread.Turns[index]
		if turn.Assistant != "" || turn.Error != "" || turn.Status == surface.StatusBusy {
			return &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: turn.Done, Error: turn.Error, Source: "rpc"}
		}
	}
	for index := len(exchanges) - 1; index >= 0; index-- {
		if exchanges[index].Assistant != "" {
			return &surface.ReplyResult{Text: exchanges[index].Assistant, UserText: exchanges[index].User, Done: session == nil || session.Status != surface.StatusBusy, Source: "rpc"}
		}
	}
	return &surface.ReplyResult{Done: false, Source: "rpc"}
}
