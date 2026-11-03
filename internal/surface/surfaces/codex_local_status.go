package surfaces

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
	path := codexTranscriptPath(session)
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		session.Transcript = ""
		return
	}
	session.Transcript = path
	session.HasLocal = true
	c.applyCodexTranscriptStatus(ctx, session, info)
}

// applyCodexTranscriptStatus lets the transcript's latest task lifecycle
// override the provider status. List and LocalStatus share this rule so the
// catalog's discovery and status passes never disagree. A working session
// carries the open turn's activity even after the rollout has gone quiet
// for longer than the transcript alone counts as busy, since the runtime
// still reports the turn running.
func (c *Codex) applyCodexTranscriptStatus(ctx context.Context, session *surface.Session, info os.FileInfo) {
	session.Plan = c.plan(ctx, session.Transcript, info)
	session.BackgroundTasks = c.codexBackground(ctx, session.ID, session.Transcript, info)
	deep := session.Status == surface.StatusBusy || c.threadLoaded(session.ID)
	turn := c.liveTurn(ctx, session.ID, session.Transcript, info, deep, time.Now())
	session.Activity = nil
	if turn.status != surface.StatusUnknown {
		session.Status = turn.status
		advanceBusyActivity(session, info.ModTime())
	}
	if turn.newest.After(session.LastActive) {
		session.LastActive = turn.newest
	}
	session.LastActive = c.keepLastActive(session.ID, session.LastActive)
	if session.Status == surface.StatusBusy && session.WaitingOn == nil {
		session.Activity = turn.activity
	}
}

// LocalStatusFiles watches the pending model record even before the
// thread has a rollout, since a model can be chosen for a thread with no
// turns yet.
func (c *Codex) LocalStatusFiles(session surface.Session) []string {
	files := []string{codexPendingModelPath(session.ID)}
	if session.Transcript != "" {
		files = append(files, session.Transcript)
	}
	return files
}

func (c *Codex) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	applyCodexPendingModel(&session)
	if session.Transcript == "" {
		return session, nil
	}
	info, err := os.Stat(session.Transcript)
	if err != nil {
		return session, err
	}
	c.applyCodexTranscriptStatus(ctx, &session, info)
	return session, nil
}
