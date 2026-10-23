package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

const (
	// voiceBriefExchanges is how many of a session's newest exchanges a voice
	// brief reads; the brief keeps as many as fit its byte cap.
	voiceBriefExchanges = 6
	// voiceBriefTimeout bounds the reads behind a brief, so a slow provider
	// delays a call's start by at most this much.
	voiceBriefTimeout = 3 * time.Second
)

// voiceBriefs reads a voice brief from the session's journal and its
// provider's goal and context records.
type voiceBriefs struct{ d *Daemon }

func (b voiceBriefs) BriefSource(ctx context.Context, session *surface.Session) voice.BriefSource {
	ctx, cancel := context.WithTimeout(ctx, voiceBriefTimeout)
	defer cancel()
	src := voice.BriefSource{Session: *session, AsOf: time.Now()}
	missing := func(part string, err error) { src.Missing = append(src.Missing, part+": "+err.Error()) }
	if page, err := b.d.readJournalPage(session.ID, 0, voiceBriefExchanges); err != nil {
		missing("recent exchanges", err)
	} else {
		src.Exchanges, src.JournalSeq = page.Exchanges, page.JournalSeq
		if page.UnavailableReason != "" {
			src.Missing = append(src.Missing, "recent exchanges: "+page.UnavailableReason)
		}
	}
	adapter, err := b.d.adapters().For(session)
	if err != nil {
		missing("goal and compactions", err)
		return src
	}
	if adapter.Capabilities().GoalStatus {
		if goal, err := adapter.GoalGet(ctx, session); err != nil {
			missing("goal", err)
		} else {
			src.Goal = goal
		}
	} else {
		src.Missing = append(src.Missing, "goal: this session does not report one")
	}
	if usage, ok := adapter.(surface.ContextUsageProvider); ok {
		if current, err := usage.ContextUsage(ctx, session); err != nil {
			missing("compactions", err)
		} else if current == nil {
			src.Missing = append(src.Missing, "compactions: not reported yet")
		} else {
			count := current.CompactionCount
			src.Compactions = &count
		}
	} else {
		src.Missing = append(src.Missing, "compactions: this session does not report them")
	}
	return src
}
