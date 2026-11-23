package codexsurface

import (
	"context"
	"errors"
	"io/fs"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
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

// ReadSession uses the local transcript for Desktop content and the managed app-server for
// managed exchanges; timeline items always come from the local transcript.
func (c *Codex) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path, info, release, err := c.home.statTranscript(session)
	defer release()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	page, err := readCodexTranscriptPage(ctx, c.home, path, session.ID, request.Before, request.Limit)
	if err != nil {
		return nil, err
	}
	if compressed, ok := info.(compressedRolloutInfo); ok && page.TranscriptIdentity != "" {
		page.TranscriptIdentity = transcript.FileIdentity(compressed)
	}
	local := surface.BoundSessionRead(session, page)
	if request.Before > 0 {
		return local, nil
	}
	if !c.managed {
		return local, nil
	}
	remote, remoteErr := c.readSessionFromRPC(ctx, session, request.Limit, codexRecordedTurns(local.Items))
	if remoteErr == nil {
		remote.Items = local.Items
		remote.NextBefore = local.NextBefore
		remote.Truncated = local.Truncated
		remote.UnavailableReason = local.UnavailableReason
		remote.TranscriptOffset = local.TranscriptOffset
		remote.TranscriptOffsetSet = local.TranscriptOffsetSet
		remote.TranscriptIdentity = local.TranscriptIdentity
		remote.CodexPendingEventUser = local.CodexPendingEventUser
		remote.CodexPendingEventTurn = local.CodexPendingEventTurn
		remote.CodexCurrentTurnID = local.CodexCurrentTurnID
		remote.CodexToolWindow = local.CodexToolWindow
		return remote, nil
	}
	failure := codexNativeReadFailure(remoteErr)
	if local.UnavailableReason != "" {
		local.UnavailableReason += " " + failure
	} else {
		local.Warning = failure + " Showing the local transcript instead."
	}
	return local, nil
}

func codexNativeReadFailure(err error) string {
	if isCodexTimeout(err) {
		return "Codex Desktop did not answer the native session read in time."
	}
	return "Codex Desktop native session read failed."
}
