package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

const (
	journalAgeCap   = 90 * 24 * time.Hour
	journalCapEvery = time.Hour
)

func (d *Daemon) runJournalCaps(ctx context.Context) {
	ticker := time.NewTicker(journalCapEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.capJournals(ctx, now)
		}
	}
}

func (d *Daemon) capJournals(ctx context.Context, now time.Time) {
	before := now.Add(-journalAgeCap)
	stale, err := d.Registry.StaleJournalSessions(before)
	if err != nil {
		d.log.Printf("journal cap: list stale journals: %s", err)
		return
	}
	if len(stale) == 0 {
		return
	}
	asked, err := voice.RequestTargets(d.voiceStatePath())
	if err != nil {
		d.log.Printf("journal cap: read voice requests: %s", err)
		return
	}
	dropped, freed := 0, int64(0)
	for _, journal := range stale {
		if asked[journal.SessionID] || d.sources.open(journal.SessionID) || !d.transcriptOnDisk(journal.SessionID) {
			continue
		}
		ok, err := d.Registry.DropStaleJournal(journal, before)
		if err != nil {
			d.log.Printf("journal cap: session %s: %s", journal.SessionID, err)
		}
		if ok {
			dropped++
			freed += journal.Bytes
		}
		if !pauseJournalSweep(ctx) {
			break
		}
	}
	if dropped > 0 {
		d.log.Printf("journal cap: dropped %d journals idle over %s, %d bytes", dropped, journalAgeCap, freed)
	}
}

func (d *Daemon) transcriptOnDisk(sessionID string) bool {
	session, err := d.Registry.Session(sessionID)
	if err != nil || session == nil {
		return false
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return false
	}
	checker, ok := adapter.(surface.TranscriptChecker)
	return ok && checker.TranscriptOnDisk(session)
}

func (d *Daemon) voiceStatePath() string {
	return filepath.Join(filepath.Dir(d.Registry.Path()), "voice", "operator.json")
}
