package codexsurface

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexStatusReadChunk = int64(64 << 10)
	codexStatusReadLimit = int64(4 << 20)
	codexBusyFreshness   = 15 * time.Minute
)

func (c *Codex) reconcileLocalStatus(ctx context.Context, session *surface.Session) {
	source := c.home.rolloutFile(session)
	if source == "" {
		return
	}
	info, err := os.Stat(source)
	if err != nil {
		session.Transcript = ""
		return
	}
	session.Transcript = source
	session.HasLocal = true
	if codexCompressed(source) {
		session.SettledTranscript = true
		return
	}
	c.applyCodexTranscriptStatus(ctx, session, source, info)
}

// applyCodexTranscriptStatus lets the transcript's latest task lifecycle
// override the provider status. List and LocalStatus share this rule so the
// catalog's discovery and status passes never disagree. A working session
// carries the open turn's activity even after the rollout has gone quiet
// for longer than the transcript alone counts as busy, since the runtime
// still reports the turn running.
func (c *Codex) applyCodexTranscriptStatus(ctx context.Context, session *surface.Session, path string, info os.FileInfo) {
	session.Plan = c.plan(ctx, path, info)
	session.BackgroundTasks = c.codexBackground(ctx, session.ID, path, info)
	deep := session.Status == surface.StatusBusy || c.threadLoaded(session.ID)
	turn := c.liveTurn(ctx, session.ID, path, info, deep, time.Now())
	session.Activity, session.TurnStop = nil, turn.stop
	if turn.status != surface.StatusUnknown {
		session.Status = turn.status
		surface.AdvanceBusyActivity(session, info.ModTime())
	}
	if session.Status == surface.StatusBusy {
		session.Ended = false
	}
	if turn.newest.After(session.LastActive) {
		session.LastActive = turn.newest
	}
	session.LastActive = c.keepLastActive(session.ID, session.LastActive)
	if session.Status == surface.StatusBusy && session.WaitingOn == nil {
		session.Activity = turn.activity
	}
}

func (c *Codex) LocalStatusFiles(session surface.Session) []string {
	if session.Transcript == "" {
		return nil
	}
	return []string{session.Transcript}
}

func (c *Codex) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	if session.Transcript == "" {
		return session, nil
	}
	path, info, err := c.home.statRollout(&session)
	if err != nil {
		return session, err
	}
	if codexCompressed(path) {
		return session, nil
	}
	c.applyCodexTranscriptStatus(ctx, &session, path, info)
	return session, nil
}
