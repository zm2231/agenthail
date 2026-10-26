package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path, err := c.sessionTranscript(session)
	if err != nil {
		return nil, err
	}
	page, err := readTranscriptPage(ctx, path, "claude", request.Before, request.Limit, func(at time.Time) bool { return c.settledAfter(session, at) })
	if err != nil {
		return nil, err
	}
	return surface.BoundSessionRead(session, page), nil
}

// ReadSession uses the local transcript for Desktop content and the managed app-server for
// managed exchanges; timeline items always come from the local transcript.
func (c *Codex) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	page, err := readCodexTranscriptPage(ctx, codexTranscriptPath(session), session.ID, request.Before, request.Limit)
	if err != nil {
		return nil, err
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
